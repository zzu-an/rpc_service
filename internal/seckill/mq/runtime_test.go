package mq

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"

	"service_rpc/internal/config"
)

type runtimeReader struct {
	messages chan kafka.Message
	closed   chan struct{}
	mu       sync.Mutex
	commits  []kafka.Message
	fetchErr int
}

func newRuntimeReader(messages ...kafka.Message) *runtimeReader {
	r := &runtimeReader{messages: make(chan kafka.Message, len(messages)), closed: make(chan struct{})}
	for _, message := range messages {
		r.messages <- message
	}
	return r
}
func (r *runtimeReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	r.mu.Lock()
	if r.fetchErr > 0 {
		r.fetchErr--
		r.mu.Unlock()
		return kafka.Message{}, errors.New("temporary fetch failure")
	}
	r.mu.Unlock()
	select {
	case message := <-r.messages:
		return message, nil
	case <-r.closed:
		return kafka.Message{}, errors.New("closed")
	case <-ctx.Done():
		return kafka.Message{}, ctx.Err()
	}
}
func (r *runtimeReader) CommitMessages(_ context.Context, messages ...kafka.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commits = append(r.commits, messages...)
	return nil
}
func (r *runtimeReader) Close() error {
	select {
	case <-r.closed:
	default:
		close(r.closed)
	}
	return nil
}

type runtimeHandler struct {
	mu        sync.Mutex
	calls     int
	failFirst bool
}

func (h *runtimeHandler) Handle(context.Context, string, []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls++
	if h.failFirst && h.calls == 1 {
		return errors.New("temporary")
	}
	return nil
}

func TestRuntimeConsumerDoesNotCommitBeforeHandlerRecovery(t *testing.T) {
	reader := newRuntimeReader(kafka.Message{Key: []byte("S-1"), Value: []byte(`{}`), Offset: 1})
	handler := &runtimeHandler{failFirst: true}
	runtime := &Runtime{cfg: config.KafkaConfig{ConsumerConcurrency: 1}, handler: handler}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { runtime.consume(ctx, ctx, reader, make(chan struct{}, 1)); close(done) }()
	time.Sleep(350 * time.Millisecond)
	cancel()
	_ = reader.Close()
	<-done
	reader.mu.Lock()
	commits := len(reader.commits)
	reader.mu.Unlock()
	if handler.calls < 2 || commits != 1 {
		t.Fatalf("handler calls=%d commits=%d", handler.calls, commits)
	}
}

func TestRuntimeConsumerRecoversAfterTransientFetchError(t *testing.T) {
	reader := newRuntimeReader(kafka.Message{Key: []byte("S-1"), Value: []byte(`{}`), Offset: 1})
	reader.fetchErr = 1
	handler := &runtimeHandler{}
	runtime := &Runtime{cfg: config.KafkaConfig{ConsumerConcurrency: 1}, handler: handler}
	fetchCtx, cancelFetch := context.WithCancel(context.Background())
	processCtx, cancelProcess := context.WithCancel(context.Background())
	defer cancelProcess()
	done := make(chan struct{})
	go func() { runtime.consume(fetchCtx, processCtx, reader, make(chan struct{}, 1)); close(done) }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		reader.mu.Lock()
		commits := len(reader.commits)
		reader.mu.Unlock()
		if commits == 1 {
			cancelFetch()
			_ = reader.Close()
			<-done
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancelFetch()
	_ = reader.Close()
	<-done
	t.Fatal("message was not committed after transient fetch recovery")
}
