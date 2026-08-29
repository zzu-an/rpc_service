package mq

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestOrderRequestedV1RoundTrip(t *testing.T) {
	reservedAt := time.Date(2026, 8, 29, 1, 2, 3, 456000000, time.UTC)
	event, err := NewOrderRequestedV1("S-001", 7, 9, reservedAt, reservedAt.Add(time.Millisecond))
	if err != nil {
		t.Fatalf("NewOrderRequestedV1() error = %v", err)
	}
	payload, err := EncodeOrderRequestedV1(event)
	if err != nil {
		t.Fatalf("EncodeOrderRequestedV1() error = %v", err)
	}
	got, err := DecodeOrderRequestedV1(payload)
	if err != nil {
		t.Fatalf("DecodeOrderRequestedV1() error = %v", err)
	}
	if got != event || got.MessageKey() != "S-001" || !got.ReservedAt().Equal(reservedAt) {
		t.Fatalf("roundtrip = %+v, want %+v", got, event)
	}
}

func TestJobFactoryBuildIsDeterministicForReplay(t *testing.T) {
	factory := NewJobFactory()
	reservedAt := time.Date(2026, 8, 29, 1, 2, 3, 456000000, time.UTC)
	firstID, firstPayload, err := factory.Build("S-replay", 7, 9, reservedAt)
	if err != nil {
		t.Fatal(err)
	}
	// 即使两次调用之间经过时间，首次冻结的输入相同就必须得到逐字节相同的 payload，
	// 否则 MySQL 唯一键只能发现重复，却无法证明它是不是身份冲突。
	time.Sleep(time.Millisecond)
	secondID, secondPayload, err := factory.Build("S-replay", 7, 9, reservedAt)
	if err != nil {
		t.Fatal(err)
	}
	if firstID != secondID || string(firstPayload) != string(secondPayload) {
		t.Fatalf("replayed message drifted: id=%q/%q payload=%s/%s", firstID, secondID, firstPayload, secondPayload)
	}
}

func TestOrderRequestedV1AllowsUnknownFields(t *testing.T) {
	payload := []byte(`{"event_id":"seckill-order-requested:S-1","event_type":"seckill.order.requested","schema_version":1,"order_no":"S-1","user_id":1,"item_id":2,"reserved_at_ms":1,"occurred_at_ms":1,"attempt":0,"future_optional":"ok"}`)
	if _, err := DecodeOrderRequestedV1(payload); err != nil {
		t.Fatalf("DecodeOrderRequestedV1() error = %v", err)
	}
}

func TestOrderRequestedV1RejectsInvalidOrUnsupported(t *testing.T) {
	for _, payload := range [][]byte{
		nil,
		[]byte(`not-json`),
		[]byte(`{"schema_version":1}`),
	} {
		if _, err := DecodeOrderRequestedV1(payload); !errors.Is(err, ErrInvalidMessage) && !errors.Is(err, ErrUnsupportedMessage) {
			t.Fatalf("payload %q error = %v", payload, err)
		}
	}
	unsupported := []byte(`{"event_id":"seckill-order-requested:S-1","event_type":"seckill.order.requested","schema_version":2,"order_no":"S-1","user_id":1,"item_id":2,"reserved_at_ms":1,"occurred_at_ms":1,"attempt":0}`)
	if _, err := DecodeOrderRequestedV1(unsupported); !errors.Is(err, ErrUnsupportedMessage) {
		t.Fatalf("unsupported version error = %v", err)
	}
	if strings.Contains(string(unsupported), "password") {
		t.Fatal("message fixture unexpectedly contains credentials")
	}
}
