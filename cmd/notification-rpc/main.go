// Command notification-rpc owns notification tables; it never reads order tables.
package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"
	"time"

	notificationv1 "service_rpc/api/gen/notification/v1"
	"service_rpc/internal/config"
	"service_rpc/internal/notification"
	notificationmysql "service_rpc/internal/notification/mysqlrepo"
	notificationrpc "service_rpc/internal/notification/rpcserver"
	"service_rpc/internal/platform/database"
	platformrpc "service_rpc/internal/platform/rpc"

	"github.com/zeromicro/go-zero/core/conf"
	"google.golang.org/grpc"
)

var configFile = flag.String("f", "etc/notification-rpc.yaml", "notification rpc config file")

type appConfig struct {
	config.RPCServerConfig
	MySQL config.MySQLConfig
}

func main() {
	flag.Parse()
	var c appConfig
	conf.MustLoad(*configFile, &c)
	if err := c.RPCServerConfig.Validate(); err != nil {
		log.Fatalf("validate notification-rpc config: %v", err)
	}
	rootContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	startupContext, cancel := context.WithTimeout(rootContext, 5*time.Second)
	db, err := database.OpenMySQL(startupContext, c.MySQL)
	cancel()
	if err != nil {
		log.Fatalf("initialize notification MySQL: %v", err)
	}
	defer db.Close()
	service, err := notification.NewService(notificationmysql.New(db))
	if err != nil {
		log.Fatal(err)
	}
	server, err := platformrpc.NewServer(c.RPCServerConfig, func(grpcServer *grpc.Server) {
		notificationv1.RegisterNotificationServiceServer(grpcServer, notificationrpc.New(service))
	})
	if err != nil {
		log.Fatalf("initialize notification-rpc: %v", err)
	}
	log.Printf("starting notification-rpc %s", c.RPCServerConfig.SafeSummary())
	server.Start()
}
