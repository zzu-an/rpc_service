package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"
	"time"

	productv1 "service_rpc/api/gen/product/v1"
	"service_rpc/internal/config"
	"service_rpc/internal/platform/database"
	platformrpc "service_rpc/internal/platform/rpc"
	"service_rpc/internal/product"
	productmysql "service_rpc/internal/product/mysqlrepo"
	productrpc "service_rpc/internal/product/rpcserver"

	"github.com/zeromicro/go-zero/core/conf"
	"google.golang.org/grpc"
)

var configFile = flag.String("f", "etc/product-rpc.yaml", "product rpc config file")

type appConfig struct {
	config.RPCServerConfig
	MySQL config.MySQLConfig
}

func main() {
	flag.Parse()
	var c appConfig
	conf.MustLoad(*configFile, &c)
	if err := c.RPCServerConfig.Validate(); err != nil {
		log.Fatalf("validate product-rpc config: %v", err)
	}
	rootContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	startupContext, cancel := context.WithTimeout(rootContext, 5*time.Second)
	db, err := database.OpenMySQL(startupContext, c.MySQL)
	cancel()
	if err != nil {
		log.Fatalf("initialize product MySQL: %v", err)
	}
	defer db.Close()

	// product-rpc 只装配商品 repository；订单与 inventory 只能通过 snapshot RPC 获取商品事实。
	implementation := productrpc.New(product.NewService(productmysql.New(db)))
	server, err := platformrpc.NewServer(c.RPCServerConfig, func(grpcServer *grpc.Server) {
		productv1.RegisterProductServiceServer(grpcServer, implementation)
	})
	if err != nil {
		log.Fatalf("initialize product-rpc: %v", err)
	}
	log.Printf("starting product-rpc %s", c.RPCServerConfig.SafeSummary())
	server.Start()
}
