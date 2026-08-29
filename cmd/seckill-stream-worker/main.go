// Command seckill-stream-worker runs the v0.4.2 Redis Stream consumer.
// Kafka 方案位于独立分支，本进程只承担 Stream 到 MySQL 的受控异步落单。
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
	platformcache "service_rpc/internal/platform/cache"
	"service_rpc/internal/platform/database"
	"service_rpc/internal/seckill"
	seckillmysql "service_rpc/internal/seckill/mysqlrepo"
	"service_rpc/internal/seckill/streamqueue"
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
		return fmt.Errorf("load stream worker config: %w", err)
	}
	if err := cfg.Redis.Validate(); err != nil {
		return fmt.Errorf("validate Redis: %w", err)
	}
	if err := cfg.RedisStream.Validate(); err != nil {
		return fmt.Errorf("validate Redis Stream: %w", err)
	}
	if cfg.MySQL.MaxOpenConns <= cfg.RedisStream.ConsumerConcurrency {
		return fmt.Errorf("MySQL MaxOpenConns must exceed Redis Stream ConsumerConcurrency")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	db, err := database.OpenMySQL(ctx, cfg.MySQL)
	if err != nil {
		return fmt.Errorf("initialize stream worker MySQL: %w", err)
	}
	defer db.Close()
	redisClient, err := platformcache.OpenRedis(ctx, cfg.Redis)
	if err != nil {
		return fmt.Errorf("initialize stream worker Redis: %w", err)
	}
	defer redisClient.Close()

	repository := seckillmysql.NewWithStockMode(db, seckillmysql.StockModeAtomic)
	service := seckill.NewService(repository)
	runtime, err := streamqueue.NewRuntime(redisClient, repository, service, runtimeConfig(cfg.RedisStream))
	if err != nil {
		return err
	}
	log.Printf("starting seckill Stream worker group=%s concurrency=%d", cfg.RedisStream.ConsumerGroup, cfg.RedisStream.ConsumerConcurrency)
	if err := runtime.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func runtimeConfig(cfg config.RedisStreamConfig) streamqueue.RuntimeConfig {
	return streamqueue.RuntimeConfig{
		ConsumerGroup: cfg.ConsumerGroup, ConsumerPrefix: cfg.ConsumerPrefix,
		ConsumerConcurrency: cfg.ConsumerConcurrency, BatchSize: int64(cfg.BatchSize),
		Block: cfg.Block(), ClaimIdle: cfg.ClaimIdle(), DiscoveryInterval: cfg.DiscoveryInterval(),
		ShutdownTimeout: cfg.ShutdownTimeout(), MaxDeliveries: cfg.MaxDeliveries,
		Retention: cfg.Retention(),
	}
}
