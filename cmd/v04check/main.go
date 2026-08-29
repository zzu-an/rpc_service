// Command v04check reports read-only v0.4 job, Kafka lag, and optional item state.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"sort"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/zeromicro/go-zero/core/conf"

	"service_rpc/internal/config"
	platformcache "service_rpc/internal/platform/cache"
	"service_rpc/internal/platform/database"
	seckillmysql "service_rpc/internal/seckill/mysqlrepo"
	"service_rpc/internal/seckill/redisgate"
)

var (
	configFile = flag.String("f", "etc/store-api.yaml", "the config file")
	itemID     = flag.Uint64("item", 0, "optional seckill item ID")
)

type partitionLag struct {
	Topic           string `json:"topic"`
	Group           string `json:"group"`
	Partition       int    `json:"partition"`
	CommittedOffset int64  `json:"committed_offset"`
	EndOffset       int64  `json:"end_offset"`
	Lag             int64  `json:"lag"`
}

type itemReport struct {
	ItemID         uint64 `json:"item_id"`
	DBStock        int64  `json:"db_stock"`
	DBClaims       int64  `json:"db_claims"`
	RedisStock     int64  `json:"redis_stock"`
	RedisBuyers    int64  `json:"redis_buyers"`
	SnapshotNotice string `json:"snapshot_notice"`
}

type report struct {
	GeneratedAt time.Time             `json:"generated_at"`
	Notice      string                `json:"notice"`
	Jobs        seckillmysql.JobStats `json:"jobs"`
	Kafka       []partitionLag        `json:"kafka"`
	Item        *itemReport           `json:"item,omitempty"`
}

func main() {
	flag.Parse()
	if err := run(os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func run(output interface{ Write([]byte) (int, error) }) error {
	var cfg config.Config
	if err := conf.Load(*configFile, &cfg); err != nil {
		return fmt.Errorf("load v0.4 check config: %w", err)
	}
	if err := cfg.Kafka.Validate(); err != nil {
		return fmt.Errorf("validate Kafka: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := database.OpenMySQL(ctx, cfg.MySQL)
	if err != nil {
		return fmt.Errorf("open MySQL: %w", err)
	}
	defer db.Close()
	repository := seckillmysql.New(db)
	stats, err := repository.InspectJobStats(ctx)
	if err != nil {
		return err
	}
	result := report{
		GeneratedAt: time.Now().UTC(), Jobs: stats,
		Notice: "MySQL, Kafka, and Redis are read sequentially; this report is not a cross-system atomic snapshot. Kafka lag is transport backlog, not a count of failed orders.",
	}
	for _, target := range []struct{ topic, group string }{
		{cfg.Kafka.MainTopic, cfg.Kafka.ConsumerGroup + "-main"},
		{cfg.Kafka.RetryTopic, cfg.Kafka.ConsumerGroup + "-retry"},
	} {
		lag, err := collectKafkaLag(ctx, cfg.Kafka, target.topic, target.group)
		if err != nil {
			return err
		}
		result.Kafka = append(result.Kafka, lag...)
	}
	if *itemID != 0 {
		redisClient, err := platformcache.OpenRedis(ctx, cfg.Redis)
		if err != nil {
			return err
		}
		defer redisClient.Close()
		gate, err := redisgate.New(redisClient, cfg.Redis.OperationTimeout())
		if err != nil {
			return err
		}
		dbState, err := repository.InspectItemState(ctx, *itemID)
		if err != nil {
			return err
		}
		redisState, err := gate.InspectItem(ctx, *itemID)
		if err != nil {
			return err
		}
		result.Item = &itemReport{ItemID: *itemID, DBStock: dbState.AvailableStock, DBClaims: dbState.ClaimCount, RedisStock: redisState.Stock, RedisBuyers: redisState.BuyerCount, SnapshotNotice: "sequential diagnostic reads; never use this command to repair stock"}
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func collectKafkaLag(ctx context.Context, cfg config.KafkaConfig, topic, group string) ([]partitionLag, error) {
	client := &kafka.Client{Addr: kafka.TCP(cfg.Brokers...), Timeout: cfg.OperationTimeout()}
	committed, err := client.ConsumerOffsets(ctx, kafka.TopicAndGroup{Topic: topic, GroupId: group})
	if err != nil {
		return nil, fmt.Errorf("read Kafka committed offsets for %s/%s: %w", topic, group, err)
	}
	bootstrap, err := kafka.DialContext(ctx, "tcp", cfg.Brokers[0])
	if err != nil {
		return nil, fmt.Errorf("connect Kafka metadata: %w", err)
	}
	partitions, err := bootstrap.ReadPartitions(topic)
	_ = bootstrap.Close()
	if err != nil {
		return nil, fmt.Errorf("read Kafka partitions for %s: %w", topic, err)
	}
	result := make([]partitionLag, 0, len(partitions))
	for _, partition := range partitions {
		leader := net.JoinHostPort(partition.Leader.Host, fmt.Sprint(partition.Leader.Port))
		conn, err := kafka.DialLeader(ctx, "tcp", leader, topic, partition.ID)
		if err != nil {
			return nil, fmt.Errorf("connect Kafka leader %s partition %d: %w", topic, partition.ID, err)
		}
		end, err := conn.ReadLastOffset()
		_ = conn.Close()
		if err != nil {
			return nil, fmt.Errorf("read Kafka end offset %s/%d: %w", topic, partition.ID, err)
		}
		current := committed[partition.ID]
		if current < 0 {
			current = 0
		}
		result = append(result, partitionLag{Topic: topic, Group: group, Partition: partition.ID, CommittedOffset: current, EndOffset: end, Lag: calculateLag(current, end)})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Partition < result[j].Partition })
	return result, nil
}

func calculateLag(committed, end int64) int64 {
	if committed < 0 {
		committed = 0
	}
	if end <= committed {
		return 0
	}
	return end - committed
}
