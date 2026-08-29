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
	platformcache "service_rpc/internal/platform/cache"
	"service_rpc/internal/platform/database"
	"service_rpc/internal/product"
	productmysql "service_rpc/internal/product/mysqlrepo"
	"service_rpc/internal/rbac"
	rbacmysql "service_rpc/internal/rbac/mysqlrepo"
	"service_rpc/internal/seckill"
	seckillmq "service_rpc/internal/seckill/mq"
	seckillmysql "service_rpc/internal/seckill/mysqlrepo"
	"service_rpc/internal/seckill/redisgate"
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

	stockMode, err := seckillmysql.ParseStockMode(c.Seckill.StockMode)
	if err != nil {
		log.Fatalf("initialize seckill stock mode: %v", err)
	}
	admissionMode, err := config.ParseAdmissionMode(c.Seckill.AdmissionMode)
	if err != nil {
		log.Fatalf("initialize seckill admission mode: %v", err)
	}
	orderMode, err := config.ParseOrderMode(c.Seckill.OrderMode)
	if err != nil {
		log.Fatalf("initialize seckill order mode: %v", err)
	}
	if orderMode == config.OrderModeAsync && admissionMode != config.AdmissionModeRedis {
		// async job 必须由 Redis 先确定唯一资格/orderNo。允许 mysql+async 会绕过 v0.3
		// 的削峰与 stable buyer 语义，失败重试也无法恢复第一次预留。
		log.Fatal("initialize async seckill: admission mode must be redis")
	}
	seckillRepository := seckillmysql.NewWithStockMode(db, stockMode)
	seckillService := seckill.NewService(seckillRepository)
	if admissionMode == config.AdmissionModeRedis {
		redisClient, err := platformcache.OpenRedis(context.Background(), c.Redis)
		if err != nil {
			// Redis 模式启动失败必须终止，不能静默切回 MySQL；后者会把峰值重新压到热点行。
			log.Fatalf("initialize Redis: %v", err)
		}
		defer func() {
			if err := redisClient.Close(); err != nil {
				log.Printf("close Redis: %v", err)
			}
		}()
		gate, err := redisgate.New(redisClient, c.Redis.OperationTimeout())
		if err != nil {
			log.Fatalf("initialize Redis seckill gate: %v", err)
		}
		if orderMode == config.OrderModeAsync {
			seckillService, err = seckill.NewServiceWithAsyncAdmission(
				seckillRepository, seckillRepository, gate, gate, seckillRepository, seckillmq.NewJobFactory(),
			)
		} else {
			seckillService, err = seckill.NewServiceWithAdmission(seckillRepository, seckillRepository, gate, gate)
		}
		if err != nil {
			log.Fatalf("initialize cached seckill service: %v", err)
		}
	}
	// 两种策略都只允许在启动时选择；HTTP 请求不能指定内部并发控制或绕过 Redis。
	handler.RegisterSeckillAdminRoutes(server, tokenManager, rbacService, seckillService)
	handler.RegisterSeckillOrderRoutes(server, tokenManager, seckillService)

	fmt.Printf("Starting %s at %s:%d with seckill stock mode %s, admission mode %s, and order mode %s...\n", c.Name, c.Host, c.Port, stockMode, admissionMode, orderMode)
	server.Start()
}
