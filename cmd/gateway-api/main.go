// Command gateway-api is the only public HTTP process in v0.5.
// It owns JWT parsing and HTTP compatibility, but no MySQL/Redis credentials or business repository.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os/signal"
	"syscall"
	"time"

	"service_rpc/internal/auth"
	"service_rpc/internal/config"
	"service_rpc/internal/handler"
	notificationclient "service_rpc/internal/notification/rpcclient"
	orderclient "service_rpc/internal/order/rpcclient"
	productclient "service_rpc/internal/product/rpcclient"
	"service_rpc/internal/seckill/inventoryclient"
	seckillclient "service_rpc/internal/seckill/seckillclient"
	userclient "service_rpc/internal/user/rpcclient"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"
)

var configFile = flag.String("f", "etc/gateway-api.yaml", "gateway api config file")

type appConfig struct {
	rest.RestConf
	Auth            config.AuthConfig
	UserRPC         config.RPCClientConfig
	ProductRPC      config.RPCClientConfig
	InventoryRPC    config.RPCClientConfig
	SeckillRPC      config.RPCClientConfig
	OrderRPC        config.RPCClientConfig
	NotificationRPC config.RPCClientConfig
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
		return fmt.Errorf("load gateway config: %w", err)
	}
	for name, client := range map[string]config.RPCClientConfig{
		"user": c.UserRPC, "product": c.ProductRPC, "inventory": c.InventoryRPC, "seckill": c.SeckillRPC, "order": c.OrderRPC, "notification": c.NotificationRPC,
	} {
		if err := client.Validate(); err != nil {
			return fmt.Errorf("validate %s-rpc client: %w", name, err)
		}
	}
	tokens, err := auth.NewTokenManager(c.Auth.AccessSecret, time.Duration(c.Auth.AccessTTLSeconds)*time.Second)
	if err != nil {
		return fmt.Errorf("initialize access tokens: %w", err)
	}
	rootContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	startupContext, cancel := context.WithTimeout(rootContext, 5*time.Second)
	defer cancel()
	users, err := userclient.New(startupContext, c.UserRPC)
	if err != nil {
		return fmt.Errorf("initialize user-rpc client: %w", err)
	}
	products, err := productclient.New(startupContext, c.ProductRPC)
	if err != nil {
		return fmt.Errorf("initialize product-rpc client: %w", err)
	}
	inventory, err := inventoryclient.New(startupContext, c.InventoryRPC)
	if err != nil {
		return fmt.Errorf("initialize inventory-rpc client: %w", err)
	}
	seckillRPC, err := seckillclient.New(startupContext, c.SeckillRPC)
	if err != nil {
		return fmt.Errorf("initialize seckill-rpc client: %w", err)
	}
	orders, err := orderclient.New(startupContext, c.OrderRPC)
	if err != nil {
		return fmt.Errorf("initialize order-rpc client: %w", err)
	}
	notifications, err := notificationclient.New(startupContext, c.NotificationRPC)
	if err != nil {
		return fmt.Errorf("initialize notification-rpc client: %w", err)
	}
	cancel()

	server, err := rest.NewServer(c.RestConf)
	if err != nil {
		return fmt.Errorf("initialize gateway HTTP server: %w", err)
	}
	defer server.Stop()
	handler.RegisterRoutes(server)
	handler.RegisterGatewayRPCRoutes(server, tokens, handler.GatewayDependencies{
		Users: userAdapter{client: users}, Products: productAdapter{client: products},
		Orders: orderAdapter{client: orders}, Inventory: inventoryAdapter{client: inventory},
		Seckill:       seckillAdapter{client: seckillRPC},
		Notifications: notificationAdapter{client: notifications},
	})
	// JWT 在边缘被解析为最小 userID；原始 token 不转发给内部服务，避免扩大凭据暴露面。
	log.Printf("starting gateway-api name=%s listen=%s:%d", c.Name, c.Host, c.Port)
	server.Start()
	return nil
}
