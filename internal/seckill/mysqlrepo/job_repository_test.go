package mysqlrepo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"service_rpc/internal/seckill"
)

func TestNormalizeJobErrorCode(t *testing.T) {
	if got, err := normalizeJobErrorCode(" KAFKA_TIMEOUT "); err != nil || got != "KAFKA_TIMEOUT" {
		t.Fatalf("normalizeJobErrorCode() = %q, %v", got, err)
	}
	for _, value := range []string{"", "contains space", "contains:secret"} {
		if _, err := normalizeJobErrorCode(value); !errors.Is(err, seckill.ErrInvalidArgument) {
			t.Fatalf("normalizeJobErrorCode(%q) error = %v", value, err)
		}
	}
}

func TestEqualJSONIgnoresMySQLFormattingButDetectsValueDrift(t *testing.T) {
	original := []byte(`{"event_id":"event-1","user_id":9007199254740993,"attempt":0}`)
	mysqlFormatted := []byte(`{ "attempt": 0, "user_id": 9007199254740993, "event_id": "event-1" }`)
	if !equalJSON(original, mysqlFormatted) {
		t.Fatal("semantically identical JSON should match")
	}
	changed := []byte(`{"event_id":"event-1","user_id":9007199254740992,"attempt":0}`)
	if equalJSON(original, changed) {
		t.Fatal("large integer drift must not be hidden by float64 conversion")
	}
}

func TestJobRepositoryConcurrentEnsureAndTerminalState(t *testing.T) {
	fixture := newPurchaseFixture(t, 2, 1, true)
	repository := New(fixture.db)
	reservedAt := time.Now().UTC().Truncate(time.Microsecond)
	payload, _ := json.Marshal(map[string]any{"schema_version": 1, "order_no": "S-v04-job-test"})
	input := seckill.EnsureJobInput{
		EventID: "seckill-order-requested:S-v04-job-test", OrderNo: "S-v04-job-test",
		UserID: fixture.userIDs[0], ItemID: fixture.itemID, ReservedAt: reservedAt, Payload: payload,
	}
	t.Cleanup(func() {
		_, _ = fixture.db.ExecContext(context.Background(), `DELETE FROM seckill_order_jobs WHERE order_no = ?`, input.OrderNo)
	})

	var wg sync.WaitGroup
	ids := make(chan uint64, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			job, _, err := repository.EnsureJob(context.Background(), input)
			if err != nil {
				t.Errorf("EnsureJob() error = %v", err)
				return
			}
			ids <- job.ID
		}()
	}
	wg.Wait()
	close(ids)
	var first uint64
	for id := range ids {
		if first == 0 {
			first = id
		}
		if id != first {
			t.Fatalf("EnsureJob IDs differ: %d vs %d", id, first)
		}
	}
	var count int
	if err := fixture.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM seckill_order_jobs WHERE order_no = ?`, input.OrderNo).Scan(&count); err != nil || count != 1 {
		t.Fatalf("job count = %d, %v", count, err)
	}

	if _, _, err := repository.EnsureJob(context.Background(), seckill.EnsureJobInput{
		EventID: input.EventID, OrderNo: input.OrderNo, UserID: fixture.userIDs[0], ItemID: fixture.itemID,
		ReservedAt: input.ReservedAt.Add(time.Second), Payload: input.Payload,
	}); !errors.Is(err, seckill.ErrJobConflict) {
		t.Fatalf("identity drift error = %v", err)
	}

	if updated, err := repository.MarkJobSucceeded(context.Background(), first, time.Now()); err != nil || !updated {
		t.Fatalf("MarkJobSucceeded() = %t, %v", updated, err)
	}
	if updated, err := repository.MarkJobPublished(context.Background(), first, time.Now()); err != nil || updated {
		t.Fatalf("late MarkJobPublished() = %t, %v", updated, err)
	}
	job, err := repository.FindJobOwned(context.Background(), input.UserID, input.OrderNo)
	if err != nil || job.Status != seckill.JobStatusSucceeded {
		t.Fatalf("FindJobOwned() = %+v, %v", job, err)
	}
	if _, err := repository.FindJobOwned(context.Background(), input.UserID+1, input.OrderNo); !errors.Is(err, seckill.ErrJobNotFound) {
		t.Fatalf("other user error = %v", err)
	}

	pending, err := repository.ListPendingJobs(context.Background(), time.Now().Add(time.Second), 10)
	if err != nil {
		t.Fatalf("ListPendingJobs() error = %v", err)
	}
	for _, job := range pending {
		if job.ID == first {
			t.Fatalf("terminal job returned as pending: %s", fmt.Sprint(job.ID))
		}
	}
}
