package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"
	"time"

	userv1 "service_rpc/api/gen/user/v1"
	"service_rpc/internal/config"
	"service_rpc/internal/platform/database"
	platformrpc "service_rpc/internal/platform/rpc"
	"service_rpc/internal/rbac"
	rbacmysql "service_rpc/internal/rbac/mysqlrepo"
	"service_rpc/internal/user"
	usermysql "service_rpc/internal/user/mysqlrepo"
	userrpc "service_rpc/internal/user/rpcserver"

	"github.com/zeromicro/go-zero/core/conf"
	"google.golang.org/grpc"
)

var configFile = flag.String("f", "etc/user-rpc.yaml", "user rpc config file")

type appConfig struct {
	config.RPCServerConfig
	MySQL config.MySQLConfig
}

func main() {
	flag.Parse()
	var c appConfig
	conf.MustLoad(*configFile, &c)
	if err := c.RPCServerConfig.Validate(); err != nil {
		log.Fatalf("validate user-rpc config: %v", err)
	}

	// main 是进程取消树的根，只有这里创建根 context；所有 adapter/repository 都继续传递它的子 context。
	rootContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	startupContext, startupCancel := context.WithTimeout(rootContext, 5*time.Second)
	db, err := database.OpenMySQL(startupContext, c.MySQL)
	startupCancel()
	if err != nil {
		log.Fatalf("initialize user MySQL: %v", err)
	}
	defer db.Close()

	// 本进程只装配 user 与 RBAC repository；即使仍共用物理 MySQL，也不能导入商品/订单 repository。
	userRepository := usermysql.New(db)
	serverImplementation := userrpc.New(
		user.NewService(userRepository),
		user.NewAuthService(userRepository),
		rbac.NewService(rbacmysql.New(db)),
	)
	server, err := platformrpc.NewServer(c.RPCServerConfig, func(grpcServer *grpc.Server) {
		userv1.RegisterUserServiceServer(grpcServer, serverImplementation)
	})
	if err != nil {
		log.Fatalf("initialize user-rpc: %v", err)
	}
	log.Printf("starting user-rpc %s", c.RPCServerConfig.SafeSummary())
	server.Start()
}
