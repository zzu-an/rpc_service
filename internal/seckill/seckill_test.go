package seckill

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"service_rpc/internal/order"
)

type repositoryStub struct {
	createInput    CreateActivityInput
	addInput       AddItemInput
	statusID       uint64
	status         uint8
	purchaseUser   uint64
	purchaseItem   uint64
	purchaseNo     string
	purchaseNow    time.Time
	purchaseError  error
	purchaseResult PurchaseResult
	purchaseCalls  int
	statusError    error
	events         *[]string
}

func (r *repositoryStub) CreateActivity(_ context.Context, input CreateActivityInput) (Activity, error) {
	r.createInput = input
	return Activity{ID: 1, Name: input.Name, StartAt: input.StartAt, EndAt: input.EndAt, Status: StatusDisabled}, nil
}
func (r *repositoryStub) AddItem(_ context.Context, input AddItemInput) (Item, error) {
	r.addInput = input
	return Item{ID: 1, ActivityID: input.ActivityID, SKUID: input.SKUID, InitialStock: input.Stock, AvailableStock: input.Stock}, nil
}
func (r *repositoryStub) SetActivityStatus(_ context.Context, id uint64, status uint8) error {
	if r.events != nil {
		*r.events = append(*r.events, "mysql-status-"+string(rune('0'+status)))
	}
	r.statusID, r.status = id, status
	return r.statusError
}

type snapshotReaderStub struct {
	snapshot PreheatSnapshot
	err      error
}

func (s snapshotReaderStub) LoadPreheatSnapshot(context.Context, uint64) (PreheatSnapshot, error) {
	return s.snapshot, s.err
}

type activityCacheStub struct {
	publishResult PreheatResult
	publishErr    error
	invalidateErr error
	events        *[]string
}

func (c *activityCacheStub) PublishActivity(context.Context, PreheatSnapshot, time.Time) (PreheatResult, error) {
	return c.publishResult, c.publishErr
}

func (c *activityCacheStub) InvalidateItems(_ context.Context, _ []uint64) error {
	if c.events != nil {
		*c.events = append(*c.events, "redis-invalidate")
	}
	return c.invalidateErr
}
func (r *repositoryStub) Purchase(_ context.Context, userID, itemID uint64, orderNo string, now time.Time) (PurchaseResult, error) {
	r.purchaseCalls++
	r.purchaseUser, r.purchaseItem, r.purchaseNo, r.purchaseNow = userID, itemID, orderNo, now
	return r.purchaseResult, r.purchaseError
}

type admissionGateStub struct {
	input  ReservationInput
	result Reservation
	err    error
	calls  int
}

type jobRepositoryStub struct {
	input        EnsureJobInput
	job          OrderJob
	replayed     bool
	err          error
	markedID     uint64
	markErr      error
	foundJob     OrderJob
	foundOrder   order.Order
	findJobErr   error
	findOrderErr error
}

func (r *jobRepositoryStub) EnsureJob(_ context.Context, input EnsureJobInput) (OrderJob, bool, error) {
	r.input = input
	return r.job, r.replayed, r.err
}
func (r *jobRepositoryStub) FindJobOwned(context.Context, uint64, string) (OrderJob, error) {
	return r.foundJob, r.findJobErr
}
func (r *jobRepositoryStub) FindOrderOwned(context.Context, uint64, string) (order.Order, error) {
	return r.foundOrder, r.findOrderErr
}
func (*jobRepositoryStub) ListPendingJobs(context.Context, time.Time, int) ([]OrderJob, error) {
	return nil, nil
}
func (*jobRepositoryStub) MarkJobPublished(context.Context, uint64, time.Time) (bool, error) {
	return false, nil
}
func (*jobRepositoryStub) ScheduleJobPublishRetry(context.Context, uint64, time.Time, string) (bool, error) {
	return false, nil
}
func (r *jobRepositoryStub) MarkJobSucceeded(_ context.Context, id uint64, _ time.Time) (bool, error) {
	r.markedID = id
	return r.markErr == nil, r.markErr
}
func (*jobRepositoryStub) MarkJobFailed(context.Context, uint64, time.Time, string) (bool, error) {
	return false, nil
}

type messageFactoryStub struct {
	eventID string
	payload []byte
	err     error
}

func (f messageFactoryStub) Build(string, uint64, uint64, time.Time) (string, []byte, error) {
	return f.eventID, f.payload, f.err
}

func (g *admissionGateStub) Reserve(_ context.Context, input ReservationInput) (Reservation, error) {
	g.calls++
	g.input = input
	return g.result, g.err
}

func TestCreateActivityNormalizesAndValidates(t *testing.T) {
	repository := &repositoryStub{}
	service := NewService(repository)
	start := time.Date(2026, 8, 25, 1, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	created, err := service.CreateActivity(context.Background(), CreateActivityInput{Name: "  秋招秒杀  ", StartAt: start, EndAt: start.Add(time.Hour)})
	if err != nil {
		t.Fatalf("CreateActivity() error = %v", err)
	}
	if created.Name != "秋招秒杀" || repository.createInput.StartAt.Location() != time.UTC {
		t.Fatalf("activity was not normalized: %+v", repository.createInput)
	}

	_, err = service.CreateActivity(context.Background(), CreateActivityInput{Name: "bad", StartAt: start, EndAt: start})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid time error = %v, want ErrInvalidArgument", err)
	}
}

func TestAddItemAndStatusValidation(t *testing.T) {
	service := NewService(&repositoryStub{})
	if _, err := service.AddItem(context.Background(), AddItemInput{ActivityID: 1, SKUID: 2, Stock: 10}); err != nil {
		t.Fatalf("AddItem() error = %v", err)
	}
	if _, err := service.AddItem(context.Background(), AddItemInput{ActivityID: 1, SKUID: 2, Stock: 0}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("zero stock error = %v, want ErrInvalidArgument", err)
	}
	if err := service.SetActivityStatus(context.Background(), 1, 9); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid status error = %v, want ErrInvalidArgument", err)
	}
}

func TestPurchasePassesOneStableUTCInstant(t *testing.T) {
	repository := &repositoryStub{purchaseError: ErrOutOfStock}
	service := NewService(repository)
	wantNow := time.Date(2026, 8, 25, 2, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return wantNow }

	_, err := service.Purchase(context.Background(), 7, 9)
	if !errors.Is(err, ErrOutOfStock) {
		t.Fatalf("Purchase() error = %v, want ErrOutOfStock", err)
	}
	if repository.purchaseUser != 7 || repository.purchaseItem != 9 || repository.purchaseNo == "" || !repository.purchaseNow.Equal(wantNow) {
		t.Fatalf("unexpected repository call: user=%d item=%d no=%q now=%v", repository.purchaseUser, repository.purchaseItem, repository.purchaseNo, repository.purchaseNow)
	}
}

func TestPurchaseRejectsMissingIdentityOrItem(t *testing.T) {
	service := NewService(&repositoryStub{})
	for _, input := range [][2]uint64{{0, 1}, {1, 0}} {
		if _, err := service.Purchase(context.Background(), input[0], input[1]); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("Purchase(%d, %d) error = %v, want ErrInvalidArgument", input[0], input[1], err)
		}
	}
}

func TestPreheatActivityValidatesWindowAndPublishes(t *testing.T) {
	now := time.Date(2026, 8, 26, 1, 0, 0, 0, time.UTC)
	snapshot := PreheatSnapshot{
		Activity: Activity{ID: 1, Status: StatusEnabled, StartAt: now.Add(time.Minute), EndAt: now.Add(time.Hour)},
		Items:    []Item{{ID: 2, ActivityID: 1, SKUID: 3, InitialStock: 4, AvailableStock: 4}},
	}
	want := PreheatResult{ActivityID: 1, ItemCount: 1}
	service, err := NewServiceWithCache(&repositoryStub{}, snapshotReaderStub{snapshot: snapshot}, &activityCacheStub{publishResult: want})
	if err != nil {
		t.Fatalf("NewServiceWithCache() error = %v", err)
	}
	service.now = func() time.Time { return now }
	got, err := service.PreheatActivity(context.Background(), 1)
	if err != nil || got != want {
		t.Fatalf("PreheatActivity() = %+v, %v", got, err)
	}

	service.now = func() time.Time { return snapshot.Activity.StartAt }
	if _, err := service.PreheatActivity(context.Background(), 1); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("started PreheatActivity() error = %v", err)
	}
}

func TestCachedStatusTransitionUsesSafeOrdering(t *testing.T) {
	snapshot := PreheatSnapshot{
		Activity: Activity{ID: 1},
		Items:    []Item{{ID: 2, ActivityID: 1, SKUID: 3, InitialStock: 1, AvailableStock: 1}},
	}
	for _, tt := range []struct {
		name   string
		status uint8
		want   []string
	}{
		{name: "enable invalidates old cache first", status: StatusEnabled, want: []string{"redis-invalidate", "mysql-status-1"}},
		{name: "disable closes fact source first", status: StatusDisabled, want: []string{"mysql-status-2", "redis-invalidate"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var events []string
			repository := &repositoryStub{events: &events}
			cache := &activityCacheStub{events: &events}
			service, err := NewServiceWithCache(repository, snapshotReaderStub{snapshot: snapshot}, cache)
			if err != nil {
				t.Fatalf("NewServiceWithCache() error = %v", err)
			}
			if err := service.SetActivityStatus(context.Background(), 1, tt.status); err != nil {
				t.Fatalf("SetActivityStatus() error = %v", err)
			}
			if len(events) != len(tt.want) || events[0] != tt.want[0] || events[1] != tt.want[1] {
				t.Fatalf("events = %v, want %v", events, tt.want)
			}
		})
	}
}

func TestPurchaseUsesRedisOrderNumberAndSameInstant(t *testing.T) {
	now := time.Date(2026, 8, 26, 2, 3, 4, 0, time.UTC)
	repository := &repositoryStub{purchaseResult: PurchaseResult{}}
	gate := &admissionGateStub{result: Reservation{OrderNo: "redis-original-order", Replayed: true}}
	service, err := NewServiceWithAdmission(repository, snapshotReaderStub{}, &activityCacheStub{}, gate)
	if err != nil {
		t.Fatalf("NewServiceWithAdmission() error = %v", err)
	}
	service.now = func() time.Time { return now }
	result, err := service.Purchase(context.Background(), 7, 9)
	if err != nil {
		t.Fatalf("Purchase() error = %v", err)
	}
	if gate.input.OrderNo == "" || gate.input.OrderNo == "redis-original-order" {
		t.Fatalf("candidate order was not generated independently: %+v", gate.input)
	}
	if !gate.input.Now.Equal(now) || !repository.purchaseNow.Equal(now) {
		t.Fatalf("gate now=%v repository now=%v, want %v", gate.input.Now, repository.purchaseNow, now)
	}
	if repository.purchaseNo != "redis-original-order" || !result.Replayed {
		t.Fatalf("repository order=%q result=%+v", repository.purchaseNo, result)
	}
}

func TestPurchaseGateRejectionDoesNotCallMySQL(t *testing.T) {
	for _, gateErr := range []error{ErrCacheNotReady, ErrOutOfStock, ErrAdmissionFailure} {
		t.Run(gateErr.Error(), func(t *testing.T) {
			repository := &repositoryStub{}
			gate := &admissionGateStub{err: gateErr}
			service, err := NewServiceWithAdmission(repository, snapshotReaderStub{}, &activityCacheStub{}, gate)
			if err != nil {
				t.Fatalf("NewServiceWithAdmission() error = %v", err)
			}
			if _, err := service.Purchase(context.Background(), 7, 9); !errors.Is(err, gateErr) {
				t.Fatalf("Purchase() error = %v, want %v", err, gateErr)
			}
			if repository.purchaseCalls != 0 {
				t.Fatalf("MySQL Purchase calls = %d, want 0", repository.purchaseCalls)
			}
		})
	}
}

func TestPurchaseMySQLFailureDoesNotInvokeRedisAgain(t *testing.T) {
	repository := &repositoryStub{purchaseError: context.DeadlineExceeded}
	gate := &admissionGateStub{result: Reservation{OrderNo: "stable-order"}}
	service, err := NewServiceWithAdmission(repository, snapshotReaderStub{}, &activityCacheStub{}, gate)
	if err != nil {
		t.Fatalf("NewServiceWithAdmission() error = %v", err)
	}
	if _, err := service.Purchase(context.Background(), 7, 9); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Purchase() error = %v", err)
	}
	if gate.calls != 1 || repository.purchaseCalls != 1 || repository.purchaseNo != "stable-order" {
		t.Fatalf("gate calls=%d repository calls=%d order=%q", gate.calls, repository.purchaseCalls, repository.purchaseNo)
	}
}

func TestEnqueuePersistsStableReservationWithoutPurchase(t *testing.T) {
	now := time.Date(2026, 8, 29, 3, 4, 5, 0, time.UTC)
	repository := &repositoryStub{}
	gate := &admissionGateStub{result: Reservation{OrderNo: "stable-async-order", Replayed: true}}
	jobs := &jobRepositoryStub{replayed: true}
	service, err := NewServiceWithAsyncAdmission(
		repository, snapshotReaderStub{}, &activityCacheStub{}, gate, jobs,
		messageFactoryStub{eventID: "event-1", payload: []byte(`{"schema_version":1}`)},
	)
	if err != nil {
		t.Fatalf("NewServiceWithAsyncAdmission() error = %v", err)
	}
	service.now = func() time.Time { return now }
	result, err := service.Enqueue(context.Background(), 7, 9)
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	if result.OrderNo != "stable-async-order" || !result.Replayed || repository.purchaseCalls != 0 {
		t.Fatalf("Enqueue() = %+v purchaseCalls=%d", result, repository.purchaseCalls)
	}
	if jobs.input.EventID != "event-1" || jobs.input.OrderNo != result.OrderNo || !jobs.input.ReservedAt.Equal(now) {
		t.Fatalf("EnsureJob input = %+v", jobs.input)
	}
}

func TestReserveRecoversWinnerTimeFromReplayedOrderNumber(t *testing.T) {
	winnerTime := time.Date(2026, 8, 29, 3, 4, 5, 123000000, time.UTC)
	winnerOrder := "S" + strconv.FormatInt(winnerTime.UnixMilli(), 10) + "0123456789abcdef"
	requestTime := winnerTime.Add(5 * time.Second)
	gate := &admissionGateStub{result: Reservation{OrderNo: winnerOrder, Replayed: true}}
	service, err := NewServiceWithAdmission(&repositoryStub{}, snapshotReaderStub{}, &activityCacheStub{}, gate)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return requestTime }
	orderNo, replayed, reservedAt, err := service.reserve(context.Background(), 7, 9)
	if err != nil || !replayed || orderNo != winnerOrder || !reservedAt.Equal(winnerTime) {
		t.Fatalf("reserve() order=%q replayed=%t reserved_at=%v error=%v", orderNo, replayed, reservedAt, err)
	}
}

func TestEnqueueJobFailureKeepsSingleRedisReservation(t *testing.T) {
	repository := &repositoryStub{}
	gate := &admissionGateStub{result: Reservation{OrderNo: "stable-async-order"}}
	jobs := &jobRepositoryStub{err: context.DeadlineExceeded}
	service, err := NewServiceWithAsyncAdmission(
		repository, snapshotReaderStub{}, &activityCacheStub{}, gate, jobs,
		messageFactoryStub{eventID: "event-1", payload: []byte(`{"schema_version":1}`)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Enqueue(context.Background(), 7, 9); !errors.Is(err, ErrQueueUnavailable) {
		t.Fatalf("Enqueue() error = %v", err)
	}
	if gate.calls != 1 || repository.purchaseCalls != 0 {
		t.Fatalf("gate calls=%d purchase calls=%d", gate.calls, repository.purchaseCalls)
	}
}

func TestProcessQueuedJobUsesReservedAtAndMarksAfterPurchase(t *testing.T) {
	reservedAt := time.Date(2026, 8, 29, 3, 0, 0, 0, time.UTC)
	repository := &repositoryStub{purchaseResult: PurchaseResult{}}
	jobs := &jobRepositoryStub{}
	service := NewService(repository)
	service.jobRepository = jobs
	job := OrderJob{ID: 11, OrderNo: "S-queued", UserID: 7, ItemID: 9, ReservedAt: reservedAt, Status: JobStatusPublished}
	if _, err := service.ProcessQueuedJob(context.Background(), job); err != nil {
		t.Fatalf("ProcessQueuedJob() error = %v", err)
	}
	if repository.purchaseNo != job.OrderNo || !repository.purchaseNow.Equal(reservedAt) || jobs.markedID != job.ID {
		t.Fatalf("purchaseNo=%q purchaseNow=%v markedID=%d", repository.purchaseNo, repository.purchaseNow, jobs.markedID)
	}

	jobs.markErr = context.DeadlineExceeded
	if _, err := service.ProcessQueuedJob(context.Background(), job); err == nil {
		t.Fatal("ProcessQueuedJob() error = nil after status failure")
	}
	if repository.purchaseCalls != 2 {
		t.Fatalf("purchase calls=%d, want redelivery call", repository.purchaseCalls)
	}
}

func TestGetAsyncResultPrefersOrderAndMapsJobStates(t *testing.T) {
	created := order.Order{ID: 1, OrderNo: "S-result", UserID: 7}
	jobs := &jobRepositoryStub{foundOrder: created}
	service := NewService(&repositoryStub{})
	service.jobRepository = jobs
	got, err := service.GetAsyncResult(context.Background(), 7, created.OrderNo)
	if err != nil || got.Status != AsyncResultSucceeded || got.Order.ID != created.ID {
		t.Fatalf("succeeded result=%+v error=%v", got, err)
	}

	jobs.findOrderErr = ErrJobNotFound
	jobs.foundJob = OrderJob{OrderNo: created.OrderNo, Status: JobStatusPublished}
	got, err = service.GetAsyncResult(context.Background(), 7, created.OrderNo)
	if err != nil || got.Status != AsyncResultQueued {
		t.Fatalf("queued result=%+v error=%v", got, err)
	}
	jobs.foundJob.Status = JobStatusFailed
	got, err = service.GetAsyncResult(context.Background(), 7, created.OrderNo)
	if err != nil || got.Status != AsyncResultFailed {
		t.Fatalf("failed result=%+v error=%v", got, err)
	}
	jobs.findJobErr = ErrJobNotFound
	if _, err := service.GetAsyncResult(context.Background(), 8, created.OrderNo); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("other user result error=%v", err)
	}
}
