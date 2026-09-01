// Command seckill-orchestrator replaces the v0.4.2 direct-SQL Stream worker.
// It consumes seckill's internal Redis Stream and writes only through inventory/order RPC contracts.
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
	orderclient "service_rpc/internal/order/rpcclient"
	platformcache "service_rpc/internal/platform/cache"
	"service_rpc/internal/seckill/inventoryclient"
	seckillclient "service_rpc/internal/seckill/seckillclient"
	"service_rpc/internal/seckill/streamqueue"

	"github.com/zeromicro/go-zero/core/conf"
)

var configFile = flag.String("f", "etc/seckill-orchestrator.yaml", "seckill orchestrator config file")

type budgetConfig struct {
	TaskMilliseconds      int
	InventoryMilliseconds int
	OrderMilliseconds     int
}

type retryConfig struct {
	MaxAttempts             int
	BackoffBaseMilliseconds int
	BackoffMaxMilliseconds  int
	JitterRatio             float64
	BreakerFailures         int
	BreakerOpenMilliseconds int
}

type appConfig struct {
	Redis        config.RedisConfig
	RedisStream  config.RedisStreamConfig
	SeckillRPC   config.RPCClientConfig
	InventoryRPC config.RPCClientConfig
	OrderRPC     config.RPCClientConfig
	Budget       budgetConfig
	Retry        retryConfig
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
		return fmt.Errorf("load orchestrator config: %w", err)
	}
	if err := c.Redis.Validate(); err != nil {
		return err
	}
	if err := c.RedisStream.Validate(); err != nil {
		return err
	}
	for name, rpcConfig := range map[string]config.RPCClientConfig{"seckill": c.SeckillRPC, "inventory": c.InventoryRPC, "order": c.OrderRPC} {
		if err := rpcConfig.Validate(); err != nil {
			return fmt.Errorf("validate %s-rpc: %w", name, err)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	startupContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	redisClient, err := platformcache.OpenRedis(startupContext, c.Redis)
	if err != nil {
		cancel()
		return err
	}
	defer redisClient.Close()
	seckillRPC, err := seckillclient.New(startupContext, c.SeckillRPC)
	if err != nil {
		cancel()
		return err
	}
	inventoryRPC, err := inventoryclient.New(startupContext, c.InventoryRPC)
	if err != nil {
		cancel()
		return err
	}
	orderRPC, err := orderclient.New(startupContext, c.OrderRPC)
	cancel()
	if err != nil {
		return err
	}
	inventoryPolicy, err := newRPCPolicy(c.Retry)
	if err != nil {
		return fmt.Errorf("inventory RPC policy: %w", err)
	}
	orderPolicy, err := newRPCPolicy(c.Retry)
	if err != nil {
		return fmt.Errorf("order RPC policy: %w", err)
	}
	processor, err := streamqueue.NewRPCProcessor(inventoryRPCAdapter{client: inventoryRPC, policy: inventoryPolicy}, orderRPCAdapter{client: orderRPC, policy: orderPolicy}, streamqueue.RPCProcessorConfig{
		TaskTimeout:      time.Duration(c.Budget.TaskMilliseconds) * time.Millisecond,
		InventoryTimeout: time.Duration(c.Budget.InventoryMilliseconds) * time.Millisecond,
		OrderTimeout:     time.Duration(c.Budget.OrderMilliseconds) * time.Millisecond,
	})
	if err != nil {
		return err
	}
	runtime, err := streamqueue.NewRuntime(redisClient, streamItemSource{seckillRPC}, processor, runtimeConfig(c.RedisStream))
	if err != nil {
		return err
	}
	// Redis Stream 是 seckill 内部 command queue；Kafka 只由订单 Outbox 在事实提交后发布，
	// 所以这里不存在无收益的 Stream→Kafka→RPC bridge。
	log.Printf("starting seckill orchestrator group=%s concurrency=%d", c.RedisStream.ConsumerGroup, c.RedisStream.ConsumerConcurrency)
	return runtime.Run(ctx)
}

func runtimeConfig(c config.RedisStreamConfig) streamqueue.RuntimeConfig {
	return streamqueue.RuntimeConfig{
		ConsumerGroup: c.ConsumerGroup, ConsumerPrefix: c.ConsumerPrefix,
		ConsumerConcurrency: c.ConsumerConcurrency, BatchSize: int64(c.BatchSize),
		Block: c.Block(), ClaimIdle: c.ClaimIdle(), DiscoveryInterval: c.DiscoveryInterval(),
		ShutdownTimeout: c.ShutdownTimeout(), MaxDeliveries: c.MaxDeliveries, Retention: c.Retention(),
	}
}
