// Command seckill-result-projector consumes order facts in a group independent from notification.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os/signal"
	"syscall"
	"time"

	"service_rpc/internal/config"
	platformcache "service_rpc/internal/platform/cache"
	"service_rpc/internal/platform/mq"
	"service_rpc/internal/seckill/resultprojector"

	"github.com/zeromicro/go-zero/core/conf"
)

var configFile = flag.String("f", "etc/seckill-result-projector.yaml", "seckill result projector config file")

type appConfig struct {
	Redis config.RedisConfig
	Kafka struct {
		Brokers                      []string
		ClientID                     string
		GroupID                      string
		Topics                       []string
		RetryTopic                   string
		DLQTopic                     string
		MaxAttempts                  int
		OperationTimeoutMilliseconds int
	}
	Projection struct {
		RetentionSeconds int
	}
}

func main() {
	flag.Parse()
	if err := run(*configFile); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}

func run(path string) error {
	var c appConfig
	if err := conf.Load(path, &c); err != nil {
		return fmt.Errorf("load projector config: %w", err)
	}
	if err := c.Redis.Validate(); err != nil {
		return err
	}
	operationTimeout := time.Duration(c.Kafka.OperationTimeoutMilliseconds) * time.Millisecond
	source, err := mq.NewGroupClient(mq.KafkaConfig{
		Brokers: c.Kafka.Brokers, ClientID: c.Kafka.ClientID, GroupID: c.Kafka.GroupID,
		Topics: c.Kafka.Topics, OperationTimeout: operationTimeout,
	})
	if err != nil {
		return err
	}
	defer source.Close()
	publisher, err := mq.NewPublisher(mq.KafkaConfig{Brokers: c.Kafka.Brokers, ClientID: c.Kafka.ClientID + "-retry", OperationTimeout: operationTimeout})
	if err != nil {
		return err
	}
	defer publisher.Close()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	startupContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	redisClient, err := platformcache.OpenRedis(startupContext, c.Redis)
	if err != nil {
		cancel()
		return err
	}
	defer redisClient.Close()
	if err := source.Ping(startupContext); err != nil {
		cancel()
		return err
	}
	cancel()
	projector, err := resultprojector.New(redisClient, c.Redis.OperationTimeout(), time.Duration(c.Projection.RetentionSeconds)*time.Second)
	if err != nil {
		return err
	}
	consumer, err := mq.NewConsumer(source, publisher, resultprojector.NewOrderCreatedHandler(projector), mq.ConsumerConfig{
		RetryTopic: c.Kafka.RetryTopic, DLQTopic: c.Kafka.DLQTopic, MaxAttempts: c.Kafka.MaxAttempts,
	})
	if err != nil {
		return err
	}
	// projector 与 notification 的 group 必须不同：不同 group 是广播 fan-out；同 group 只会竞争分区。
	log.Printf("starting seckill result projector group=%s topics=%v", c.Kafka.GroupID, c.Kafka.Topics)
	return consumer.Run(ctx)
}
