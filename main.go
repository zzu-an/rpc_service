package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"

	"service_rpc/internal/auth"
	"service_rpc/internal/config"
	"service_rpc/internal/handler"
	"service_rpc/internal/order"
	ordermysql "service_rpc/internal/order/mysqlrepo"
	"service_rpc/internal/platform/database"
	"service_rpc/internal/product"
	productmysql "service_rpc/internal/product/mysqlrepo"
	"service_rpc/internal/rbac"
	rbacmysql "service_rpc/internal/rbac/mysqlrepo"
	"service_rpc/internal/user"
	usermysql "service_rpc/internal/user/mysqlrepo"
)

var configFile = flag.String("f", "etc/store-api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c)

	db, err := database.OpenMySQL(context.Background(), c.MySQL)
	if err != nil {
		log.Fatalf("initialize MySQL: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("close MySQL: %v", err)
		}
	}()

	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()
	handler.RegisterRoutes(server)

	userRepository := usermysql.New(db)
	userService := user.NewService(userRepository)
	handler.RegisterUserRoutes(server, userService)

	authService := user.NewAuthService(userRepository)
	rbacService := rbac.NewService(rbacmysql.New(db))
	tokenManager, err := auth.NewTokenManager(
		c.Auth.AccessSecret,
		time.Duration(c.Auth.AccessTTLSeconds)*time.Second,
	)
	if err != nil {
		log.Fatalf("initialize access tokens: %v", err)
	}
	handler.RegisterAuthRoutes(server, authService, tokenManager, rbacService)
	handler.RegisterRBACRoutes(server, tokenManager, rbacService)

	productService := product.NewService(productmysql.New(db))
	handler.RegisterProductRoutes(server, tokenManager, rbacService, productService)

	orderService := order.NewService(ordermysql.New(db))
	handler.RegisterOrderRoutes(server, tokenManager, orderService)

	fmt.Printf("Starting %s at %s:%d...\n", c.Name, c.Host, c.Port)
	server.Start()
}
