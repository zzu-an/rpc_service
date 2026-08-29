// Command v03check 对单个秒杀 item 做 Redis/MySQL 只读差值诊断。
// 它刻意没有 repair 子命令：结果未知时自动回补资格会把“少卖”升级成“超卖”。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/zeromicro/go-zero/core/conf"

	"service_rpc/internal/config"
	platformcache "service_rpc/internal/platform/cache"
	"service_rpc/internal/platform/database"
	seckillmysql "service_rpc/internal/seckill/mysqlrepo"
	"service_rpc/internal/seckill/redisgate"
)

type diagnosticStatus string

const (
	statusConsistent    diagnosticStatus = "CONSISTENT"
	statusReservedAhead diagnosticStatus = "RESERVED_AHEAD"
	statusCacheMissing  diagnosticStatus = "CACHE_MISSING"
	statusDangerous     diagnosticStatus = "DANGEROUS_DRIFT"
	statusUnknown       diagnosticStatus = "UNKNOWN"
)

type mysqlStateReader interface {
	InspectItemState(ctx context.Context, itemID uint64) (seckillmysql.ItemConsistencyState, error)
}

type redisStateReader interface {
	InspectItem(ctx context.Context, itemID uint64) (redisgate.ItemConsistencyState, error)
}

type report struct {
	ItemID           uint64                            `json:"item_id"`
	Status           diagnosticStatus                  `json:"status"`
	MySQL            seckillmysql.ItemConsistencyState `json:"mysql"`
	Redis            redisgate.ItemConsistencyState    `json:"redis"`
	StockDelta       int64                             `json:"mysql_available_minus_redis_stock"`
	ReservationDelta int64                             `json:"redis_buyers_minus_mysql_claims"`
	ErrorCategories  []string                          `json:"error_categories,omitempty"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("v03check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configFile := flags.String("f", "etc/store-api.yaml", "service config file")
	itemID := flags.Uint64("item", 0, "exact seckill item ID")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *itemID == 0 {
		_, _ = fmt.Fprintln(stderr, "-item must be positive")
		return 2
	}
	var cfg config.Config
	if err := conf.Load(*configFile, &cfg); err != nil {
		_, _ = fmt.Fprintln(stderr, "load config failed")
		return 2
	}
	ctx := context.Background()
	db, err := database.OpenMySQL(ctx, cfg.MySQL)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "open MySQL failed")
		return 2
	}
	defer func() { _ = db.Close() }()
	redisClient, err := platformcache.OpenRedis(ctx, cfg.Redis)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "open Redis failed")
		return 2
	}
	defer func() { _ = redisClient.Close() }()
	gate, err := redisgate.New(redisClient, cfg.Redis.OperationTimeout())
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "initialize Redis inspector failed")
		return 2
	}
	result := buildReport(ctx, *itemID, seckillmysql.New(db), gate)
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		_, _ = fmt.Fprintln(stderr, "encode report failed")
		return 2
	}
	switch result.Status {
	case statusConsistent, statusReservedAhead:
		return 0
	case statusCacheMissing:
		return 1
	default:
		return 2
	}
}

func buildReport(ctx context.Context, itemID uint64, mysqlReader mysqlStateReader, redisReader redisStateReader) report {
	result := report{ItemID: itemID}
	var mysqlErr, redisErr error
	result.MySQL, mysqlErr = mysqlReader.InspectItemState(ctx, itemID)
	result.Redis, redisErr = redisReader.InspectItem(ctx, itemID)
	if mysqlErr != nil {
		result.ErrorCategories = append(result.ErrorCategories, "mysql_read_failed")
	}
	if redisErr != nil {
		result.ErrorCategories = append(result.ErrorCategories, "redis_read_failed")
	}
	if mysqlErr != nil || redisErr != nil {
		result.Status = statusUnknown
		return result
	}
	result.StockDelta = result.MySQL.AvailableStock - result.Redis.Stock
	result.ReservationDelta = result.Redis.BuyerCount - result.MySQL.ClaimCount
	if !result.Redis.Exists {
		if result.Redis.BuyerCount > 0 {
			result.Status = statusDangerous
		} else {
			result.Status = statusCacheMissing
		}
		return result
	}
	// 正常保守预留满足：Redis stock <= DB stock，buyers >= claims，且两种差值相等。
	// 这个等式只能说明计数关系自洽，不能证明某个 buyer 一定对应哪笔订单，也不能替代对账。
	if result.Redis.Stock < 0 || result.Redis.Stock > result.MySQL.AvailableStock ||
		result.Redis.BuyerCount < result.MySQL.ClaimCount || result.Redis.BuyerCount > result.MySQL.InitialStock ||
		result.Redis.TTL <= 0 || result.StockDelta != result.ReservationDelta {
		result.Status = statusDangerous
		return result
	}
	if result.StockDelta == 0 {
		result.Status = statusConsistent
	} else {
		result.Status = statusReservedAhead
	}
	return result
}
