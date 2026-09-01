// Command seckill-rpc owns activity management and the Redis admission hot path.
// Redis Stream remains an internal queue; Kafka is introduced behind the orchestrator in TASK-066,
// so this process deliberately contains neither a Stream consumer nor a Redis->Kafka bridge.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os/signal"
	"syscall"
	"time"

	seckillv1 "service_rpc/api/gen/seckill/v1"
	"service_rpc/internal/config"
	platformcache "service_rpc/internal/platform/cache"
	"service_rpc/internal/platform/database"
	platformrpc "service_rpc/internal/platform/rpc"
	"service_rpc/internal/seckill"
	"service_rpc/internal/seckill/inventoryclient"
	seckillmysql "service_rpc/internal/seckill/mysqlrepo"
	"service_rpc/internal/seckill/redisgate"
	"service_rpc/internal/seckill/seckillrpc"

	"github.com/zeromicro/go-zero/core/conf"
	"google.golang.org/grpc"
)

var configFile = flag.String("f", "etc/seckill-rpc.yaml", "seckill rpc config file")

type appConfig struct {
	config.RPCServerConfig
	MySQL        config.MySQLConfig
	QueueRedis   config.RedisConfig
	RedisStream  config.RedisStreamConfig
	InventoryRPC config.RPCClientConfig
}

func main() {
	flag.Parse()
	if err := run(*configFile); err != nil {
		log.Fatal(err)
	}
}

func run(path string) error {
	var c appConfig
	if err := conf.Load(path, &c); err != nil {
		return fmt.Errorf("load seckill-rpc config: %w", err)
	}
	if err := c.RPCServerConfig.Validate(); err != nil {
		return fmt.Errorf("validate seckill-rpc server: %w", err)
	}
	if err := c.QueueRedis.Validate(); err != nil {
		return fmt.Errorf("validate seckill-rpc Redis: %w", err)
	}
	if err := c.RedisStream.Validate(); err != nil {
		return fmt.Errorf("validate seckill-rpc Stream: %w", err)
	}
	if err := c.InventoryRPC.Validate(); err != nil {
		return fmt.Errorf("validate inventory-rpc client: %w", err)
	}

	rootContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	startupContext, cancel := context.WithTimeout(rootContext, 5*time.Second)
	db, err := database.OpenMySQL(startupContext, c.MySQL)
	if err != nil {
		cancel()
		return fmt.Errorf("initialize seckill activity MySQL: %w", err)
	}
	defer db.Close()
	redisClient, err := platformcache.OpenRedis(startupContext, c.QueueRedis)
	if err != nil {
		cancel()
		return fmt.Errorf("initialize seckill Redis: %w", err)
	}
	defer redisClient.Close()
	inventoryRPC, err := inventoryclient.New(startupContext, c.InventoryRPC)
	cancel()
	if err != nil {
		return fmt.Errorf("initialize inventory-rpc client: %w", err)
	}

	gate, err := redisgate.New(redisClient, c.QueueRedis.OperationTimeout())
	if err != nil {
		return err
	}
	if err := gate.SetStreamRetention(c.RedisStream.Retention()); err != nil {
		return err
	}
	activities, err := seckill.NewActivityService(
		seckillmysql.NewActivityRepository(db), seckillrpc.NewInventoryItemAdapter(inventoryRPC), gate,
	)
	if err != nil {
		return err
	}
	queue, err := seckill.NewQueueService(gate, gate)
	if err != nil {
		return err
	}
	implementation := seckillrpc.NewServer(activities, queue)
	rpcServer, err := platformrpc.NewServer(c.RPCServerConfig, func(server *grpc.Server) {
		seckillv1.RegisterSeckillServiceServer(server, implementation)
	})
	if err != nil {
		return fmt.Errorf("initialize seckill-rpc: %w", err)
	}
	// 启动日志只输出安全摘要，不打印 Redis 密码、MySQL DSN 或 etcd 凭据。
	log.Printf("starting seckill-rpc %s", c.RPCServerConfig.SafeSummary())
	rpcServer.Start()
	return nil
}
