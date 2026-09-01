package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"
	"time"

	inventoryv1 "service_rpc/api/gen/inventory/v1"
	"service_rpc/internal/config"
	"service_rpc/internal/platform/database"
	platformrpc "service_rpc/internal/platform/rpc"
	productclient "service_rpc/internal/product/rpcclient"
	"service_rpc/internal/seckill"
	"service_rpc/internal/seckill/inventoryrpc"
	seckillmysql "service_rpc/internal/seckill/mysqlrepo"

	"github.com/zeromicro/go-zero/core/conf"
	"google.golang.org/grpc"
)

var configFile = flag.String("f", "etc/inventory-rpc.yaml", "inventory rpc config file")

type appConfig struct {
	config.RPCServerConfig
	MySQL      config.MySQLConfig
	ProductRPC config.RPCClientConfig
}

func main() {
	flag.Parse()
	var c appConfig
	conf.MustLoad(*configFile, &c)
	if err := c.RPCServerConfig.Validate(); err != nil {
		log.Fatalf("validate inventory-rpc config: %v", err)
	}
	if err := c.ProductRPC.Validate(); err != nil {
		log.Fatalf("validate product-rpc client config: %v", err)
	}
	rootContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	startupContext, cancel := context.WithTimeout(rootContext, 5*time.Second)
	db, err := database.OpenMySQL(startupContext, c.MySQL)
	if err != nil {
		cancel()
		log.Fatalf("initialize inventory MySQL: %v", err)
	}
	productRPC, err := productclient.New(startupContext, c.ProductRPC)
	cancel()
	if err != nil {
		_ = db.Close()
		log.Fatalf("initialize product-rpc client: %v", err)
	}
	defer db.Close()

	itemRepository := seckillmysql.NewInventoryItemRepository(db)
	reservationRepository := seckillmysql.NewReservationRepository(db)
	implementation := inventoryrpc.NewServer(
		seckill.NewInventoryItemService(itemRepository, inventoryrpc.NewProductSnapshotAdapter(productRPC)),
		seckill.NewInventoryService(reservationRepository),
	)
	// 这里只装配 inventory 自有表和 product RPC client；活动、订单、用户事实不能通过同库 repository 偷读。
	server, err := platformrpc.NewServer(c.RPCServerConfig, func(grpcServer *grpc.Server) {
		inventoryv1.RegisterInventoryServiceServer(grpcServer, implementation)
	})
	if err != nil {
		log.Fatalf("initialize inventory-rpc: %v", err)
	}
	log.Printf("starting inventory-rpc %s", c.RPCServerConfig.SafeSummary())
	server.Start()
}
