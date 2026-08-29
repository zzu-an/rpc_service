// Command seckill-worker runs the v0.4 outbox relay and Kafka consumers.
// 它与 API 共享单体领域和 MySQL，不是 v0.5 的独立 RPC 服务。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os/signal"
	"syscall"

	"github.com/zeromicro/go-zero/core/conf"

	"service_rpc/internal/config"
	"service_rpc/internal/platform/database"
	platformmq "service_rpc/internal/platform/mq"
	"service_rpc/internal/seckill"
	seckillmq "service_rpc/internal/seckill/mq"
	seckillmysql "service_rpc/internal/seckill/mysqlrepo"
)

var configFile = flag.String("f", "etc/store-api.yaml", "the config file")

func main() {
	flag.Parse()
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	var cfg config.Config
	if err := conf.Load(*configFile, &cfg); err != nil {
		return fmt.Errorf("load worker config: %w", err)
	}
	if err := cfg.Kafka.Validate(); err != nil {
		return fmt.Errorf("validate Kafka: %w", err)
	}
	if cfg.MySQL.MaxOpenConns <= cfg.Kafka.ConsumerConcurrency {
		return fmt.Errorf("MySQL MaxOpenConns must exceed Kafka ConsumerConcurrency to leave budget for job state updates")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	db, err := database.OpenMySQL(ctx, cfg.MySQL)
	if err != nil {
		return fmt.Errorf("initialize worker MySQL: %w", err)
	}
	defer db.Close()
	repository := seckillmysql.NewWithStockMode(db, seckillmysql.StockModeAtomic)
	service, err := seckill.NewWorkerService(repository, repository)
	if err != nil {
		return err
	}

	mainProducer, err := platformmq.OpenKafkaProducer(ctx, cfg.Kafka, cfg.Kafka.MainTopic)
	if err != nil {
		return err
	}
	retryProducer, err := platformmq.OpenKafkaProducer(ctx, cfg.Kafka, cfg.Kafka.RetryTopic)
	if err != nil {
		_ = mainProducer.Close()
		return err
	}
	dlqProducer, err := platformmq.OpenKafkaProducer(ctx, cfg.Kafka, cfg.Kafka.DLQTopic)
	if err != nil {
		_ = mainProducer.Close()
		_ = retryProducer.Close()
		return err
	}
	relay, err := seckillmq.NewRelay(repository, mainProducer, cfg.Kafka.RelayInterval(), 100)
	if err != nil {
		return err
	}
	consumer, err := seckillmq.NewConsumerHandler(repository, service)
	if err != nil {
		return err
	}
	delivery, err := seckillmq.NewDeliveryHandler(consumer, repository, retryProducer, dlqProducer, cfg.Kafka.MaxConsumeAttempts)
	if err != nil {
		return err
	}
	runtime, err := seckillmq.NewRuntime(cfg.Kafka, relay, delivery, mainProducer, retryProducer, dlqProducer)
	if err != nil {
		return err
	}
	log.Printf("starting seckill worker brokers=%v group=%s concurrency=%d", cfg.Kafka.Brokers, cfg.Kafka.ConsumerGroup, cfg.Kafka.ConsumerConcurrency)
	if err := runtime.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
