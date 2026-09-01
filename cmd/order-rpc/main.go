// Command order-rpc owns orders/order_items and obtains ordinary-order snapshots through product-rpc.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os/signal"
	"syscall"
	"time"

	orderv1 "service_rpc/api/gen/order/v1"
	"service_rpc/internal/config"
	"service_rpc/internal/order"
	ordermysql "service_rpc/internal/order/mysqlrepo"
	orderrpc "service_rpc/internal/order/rpcserver"
	"service_rpc/internal/platform/database"
	platformrpc "service_rpc/internal/platform/rpc"
	productclient "service_rpc/internal/product/rpcclient"

	"github.com/zeromicro/go-zero/core/conf"
	"google.golang.org/grpc"
)

var configFile = flag.String("f", "etc/order-rpc.yaml", "order rpc config file")

type appConfig struct {
	config.RPCServerConfig
	MySQL      config.MySQLConfig
	ProductRPC config.RPCClientConfig
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
		return fmt.Errorf("load order-rpc config: %w", err)
	}
	if err := c.RPCServerConfig.Validate(); err != nil {
		return fmt.Errorf("validate order-rpc server: %w", err)
	}
	if err := c.ProductRPC.Validate(); err != nil {
		return fmt.Errorf("validate product-rpc client: %w", err)
	}
	rootContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	startupContext, cancel := context.WithTimeout(rootContext, 5*time.Second)
	db, err := database.OpenMySQL(startupContext, c.MySQL)
	if err != nil {
		cancel()
		return fmt.Errorf("initialize order MySQL: %w", err)
	}
	defer db.Close()
	productRPC, err := productclient.New(startupContext, c.ProductRPC)
	cancel()
	if err != nil {
		return fmt.Errorf("initialize product-rpc client: %w", err)
	}
	repository := ordermysql.New(db)
	service, err := order.NewRPCService(repository, orderrpc.NewProductSnapshotAdapter(productRPC))
	if err != nil {
		return err
	}
	implementation := orderrpc.New(service)
	server, err := platformrpc.NewServer(c.RPCServerConfig, func(grpcServer *grpc.Server) {
		orderv1.RegisterOrderServiceServer(grpcServer, implementation)
	})
	if err != nil {
		return fmt.Errorf("initialize order-rpc: %w", err)
	}
	// 进程只装配订单 repository 与 product-rpc client；没有 inventory DB、Kafka 或 Redis。
	log.Printf("starting order-rpc %s", c.RPCServerConfig.SafeSummary())
	server.Start()
	return nil
}
