package mq

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/segmentio/kafka-go"

	"service_rpc/internal/config"
	platformmq "service_rpc/internal/platform/mq"
)

type messageReader interface {
	FetchMessage(context.Context) (kafka.Message, error)
	CommitMessages(context.Context, ...kafka.Message) error
	Close() error
}

type Runtime struct {
	cfg       config.KafkaConfig
	relay     *Relay
	handler   messageHandler
	readers   []messageReader
	producers []platformmq.Producer
}

func NewRuntime(cfg config.KafkaConfig, relay *Relay, handler messageHandler, producers ...platformmq.Producer) (*Runtime, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if relay == nil || handler == nil || len(producers) == 0 {
		return nil, fmt.Errorf("runtime relay, handler, and producers are required")
	}
	runtime := &Runtime{cfg: cfg, relay: relay, handler: handler, producers: producers}
	for _, topic := range []struct {
		name  string
		group string
	}{{cfg.MainTopic, cfg.ConsumerGroup + "-main"}, {cfg.RetryTopic, cfg.ConsumerGroup + "-retry"}} {
		for range cfg.ConsumerConcurrency {
			runtime.readers = append(runtime.readers, kafka.NewReader(kafka.ReaderConfig{
				Brokers: cfg.Brokers, GroupID: topic.group, Topic: topic.name,
				MinBytes: 1, MaxBytes: 10 << 20, MaxWait: 500 * time.Millisecond,
				StartOffset: kafka.FirstOffset,
			}))
		}
	}
	return runtime, nil
}

func (r *Runtime) Run(ctx context.Context) error {
	if r == nil {
		return fmt.Errorf("runtime is nil")
	}
	processCtx, cancelProcessing := context.WithCancel(context.Background())
	defer cancelProcessing()
	fetchCtx, cancelFetch := context.WithCancel(context.Background())
	defer cancelFetch()
	relayCtx, cancelRelay := context.WithCancel(context.Background())
	defer cancelRelay()
	sem := make(chan struct{}, r.cfg.ConsumerConcurrency)

	var consumers sync.WaitGroup
	for _, reader := range r.readers {
		reader := reader
		consumers.Add(1)
		go func() {
			defer consumers.Done()
			r.consume(fetchCtx, processCtx, reader, sem)
		}()
	}
	relayDone := make(chan struct{})
	go func() {
		defer close(relayDone)
		_ = r.relay.Run(relayCtx)
	}()

	<-ctx.Done()
	// 先停止 relay 和 Fetch，避免关闭期间继续接入新工作；Reader.Close 会唤醒阻塞 Fetch。
	cancelRelay()
	cancelFetch()
	for _, reader := range r.readers {
		_ = reader.Close()
	}

	consumerDone := make(chan struct{})
	go func() { consumers.Wait(); close(consumerDone) }()
	timer := time.NewTimer(r.cfg.ShutdownTimeout())
	defer timer.Stop()
	select {
	case <-consumerDone:
	case <-timer.C:
		// 到达优雅退出预算后取消在途 DB/Kafka 调用。未完成消息没有提交 offset，重启会重投。
		cancelProcessing()
		<-consumerDone
	}
	<-relayDone
	var closeErr error
	for _, producer := range r.producers {
		closeErr = errors.Join(closeErr, producer.Close())
	}
	return closeErr
}

func (r *Runtime) consume(fetchCtx, processCtx context.Context, reader messageReader, sem chan struct{}) {
	for {
		message, err := reader.FetchMessage(fetchCtx)
		if err != nil {
			if fetchCtx.Err() != nil {
				return
			}
			// broker、协调器或网络的瞬时错误不能永久杀死 reader，否则对应 partition
			// 只剩 lag 增长而进程表面仍存活。带退避重试避免故障时空转打满 CPU。
			if !waitContext(fetchCtx, 200*time.Millisecond) {
				return
			}
			continue
		}
		select {
		case sem <- struct{}{}:
		case <-processCtx.Done():
			return
		}
		// 一个 reader 在当前消息可靠处理/转交前不会 Fetch 下一条。仅设置 ForceCommit=false
		// 仍不够：如果并发处理的后续 offset 先提交，Kafka 会把更早失败的 offset 一并越过。
		// 显式 Fetch→Handle→Commit 顺序牺牲单 reader 内吞吐，换来可解释的至少一次语义；
		// 横向并发由多个 group reader 和 partition 提供，并受共享 semaphore 限制。
		for {
			err = r.handler.Handle(processCtx, string(message.Key), message.Value)
			if err == nil {
				break
			}
			if !waitContext(processCtx, 200*time.Millisecond) {
				<-sem
				return
			}
		}
		for {
			err = reader.CommitMessages(processCtx, message)
			if err == nil {
				break
			}
			// 数据库/目标 topic 已成功而 offset 提交失败时只重试 commit；若进程退出，
			// Kafka 会重投，数据库唯一约束再次兜底。
			if !waitContext(processCtx, 200*time.Millisecond) {
				<-sem
				return
			}
		}
		<-sem
	}
}

func waitContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
