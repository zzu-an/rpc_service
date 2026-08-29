// Command v042check 输出 v0.4.2 Redis Stream 与 MySQL 的只读诊断快照。
//
// 它只读一个 item 的精确 key，不使用 KEYS/SCAN，也不会修复或删除任何数据。不同存储
// 依次读取，因此结果只能用于判断积压方向，不能当作 Redis 与 MySQL 的原子一致性证明。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"time"

	redis "github.com/redis/go-redis/v9"
	"github.com/zeromicro/go-zero/core/conf"

	"service_rpc/internal/config"
	platformcache "service_rpc/internal/platform/cache"
	"service_rpc/internal/platform/database"
	seckillmysql "service_rpc/internal/seckill/mysqlrepo"
	"service_rpc/internal/seckill/redisgate"
)

var (
	configFile = flag.String("f", "etc/store-api.yaml", "the config file")
	itemID     = flag.Uint64("item", 0, "required seckill item ID")
)

type groupReport struct {
	Name            string `json:"name"`
	Consumers       int64  `json:"consumers"`
	Pending         int64  `json:"pending"`
	Lag             int64  `json:"lag"`
	LastDeliveredID string `json:"last_delivered_id"`
}

type streamReport struct {
	Exists      bool          `json:"exists"`
	Length      int64         `json:"length"`
	ResultCount int64         `json:"result_count"`
	RetryCount  int64         `json:"retry_count"`
	DLQLength   int64         `json:"dlq_length"`
	Groups      []groupReport `json:"groups"`
}

type report struct {
	GeneratedAt time.Time                         `json:"generated_at"`
	Notice      string                            `json:"notice"`
	ItemID      uint64                            `json:"item_id"`
	MySQL       seckillmysql.ItemConsistencyState `json:"mysql"`
	Redis       redisgate.ItemConsistencyState    `json:"redis"`
	Stream      streamReport                      `json:"stream"`
}

func main() {
	flag.Parse()
	if err := run(os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func run(output io.Writer) error {
	if *itemID == 0 {
		return fmt.Errorf("item must be positive")
	}
	var cfg config.Config
	if err := conf.Load(*configFile, &cfg); err != nil {
		return fmt.Errorf("load v0.4.2 check config: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db, err := database.OpenMySQL(ctx, cfg.MySQL)
	if err != nil {
		return fmt.Errorf("open MySQL: %w", err)
	}
	defer db.Close()
	repository := seckillmysql.New(db)
	dbState, err := repository.InspectItemState(ctx, *itemID)
	if err != nil {
		return err
	}

	client, err := platformcache.OpenRedis(ctx, cfg.Redis)
	if err != nil {
		return err
	}
	defer client.Close()
	gate, err := redisgate.New(client, cfg.Redis.OperationTimeout())
	if err != nil {
		return err
	}
	redisState, err := gate.InspectItem(ctx, *itemID)
	if err != nil {
		return err
	}
	streamState, err := inspectStream(ctx, client, *itemID)
	if err != nil {
		return err
	}

	result := report{
		GeneratedAt: time.Now().UTC(),
		Notice:      "MySQL and Redis are read sequentially; PEL means delivered but not acknowledged, while lag means not yet delivered. This command is read-only and never repairs stock.",
		ItemID:      *itemID,
		MySQL:       dbState,
		Redis:       redisState,
		Stream:      streamState,
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func inspectStream(ctx context.Context, client *redis.Client, item uint64) (streamReport, error) {
	streamKey := redisgate.StreamKey(item)
	result := streamReport{Groups: []groupReport{}}
	exists, err := client.Exists(ctx, streamKey).Result()
	if err != nil {
		return result, fmt.Errorf("inspect stream existence: %w", err)
	}
	result.Exists = exists > 0
	if result.Exists {
		result.Length, err = client.XLen(ctx, streamKey).Result()
		if err != nil {
			return result, fmt.Errorf("inspect stream length: %w", err)
		}
		groups, groupErr := client.XInfoGroups(ctx, streamKey).Result()
		if groupErr != nil && !redis.HasErrorPrefix(groupErr, "NOGROUP") {
			return result, fmt.Errorf("inspect stream groups: %w", groupErr)
		}
		result.Groups = summarizeGroups(groups)
	}
	// Hash/Stream key 不存在时 HLEN/XLEN 都安全返回 0。诊断保持只读，绝不能为了
	// 让输出“更完整”而创建 consumer group，因为 XGROUP 会改变恢复语义。
	result.ResultCount, err = client.HLen(ctx, redisgate.StreamResultsKey(item)).Result()
	if err != nil {
		return result, fmt.Errorf("inspect stream results: %w", err)
	}
	result.RetryCount, err = client.HLen(ctx, redisgate.StreamRetriesKey(item)).Result()
	if err != nil {
		return result, fmt.Errorf("inspect stream retries: %w", err)
	}
	result.DLQLength, err = client.XLen(ctx, redisgate.StreamDLQKey(item)).Result()
	if err != nil {
		return result, fmt.Errorf("inspect stream DLQ: %w", err)
	}
	return result, nil
}

func summarizeGroups(groups []redis.XInfoGroup) []groupReport {
	result := make([]groupReport, 0, len(groups))
	for _, group := range groups {
		result = append(result, groupReport{
			Name: group.Name, Consumers: group.Consumers, Pending: group.Pending,
			Lag: group.Lag, LastDeliveredID: group.LastDeliveredID,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}
