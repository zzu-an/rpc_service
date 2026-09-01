// Command v05check emits a read-only backlog/service-discovery snapshot.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	redis "github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"github.com/zeromicro/go-zero/core/conf"
	clientv3 "go.etcd.io/etcd/client/v3"

	"service_rpc/internal/config"
	platformcache "service_rpc/internal/platform/cache"
	"service_rpc/internal/platform/database"
	"service_rpc/internal/seckill/redisgate"
)

var configFile = flag.String("f", "etc/v05check.yaml", "v0.5 diagnostic config file")

type appConfig struct {
	MySQL       config.MySQLConfig
	Redis       config.RedisConfig
	StreamGroup string
	Kafka       struct {
		Brokers []string
		Topic   string
		Groups  []string
	}
	Etcd struct {
		Hosts       []string
		ServiceKeys []string
	}
	TimeoutMilliseconds int
}

type report struct {
	EtcdEndpoints        map[string][]string `json:"etcd_endpoints"`
	StreamPending        map[uint64]int64    `json:"stream_pending"`
	OutboxBacklog        int64               `json:"outbox_backlog"`
	OutboxClaimed        int64               `json:"outbox_claimed"`
	KafkaLag             map[string]int64    `json:"kafka_lag"`
	ReservationCount     int64               `json:"reservation_count"`
	SeckillOrderCount    int64               `json:"seckill_order_count"`
	ReservedWithoutOrder int64               `json:"reserved_without_order"`
}

func main() {
	flag.Parse()
	value, err := inspect(*configFile)
	if err != nil {
		log.Fatal(err)
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(encoded))
}

func inspect(path string) (report, error) {
	var c appConfig
	if err := conf.Load(path, &c); err != nil {
		return report{}, fmt.Errorf("load v0.5 diagnostic config: %w", err)
	}
	if err := validateConfig(c); err != nil {
		return report{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(c.TimeoutMilliseconds)*time.Millisecond)
	defer cancel()
	db, err := database.OpenMySQL(ctx, c.MySQL)
	if err != nil {
		return report{}, err
	}
	defer db.Close()
	redisClient, err := platformcache.OpenRedis(ctx, c.Redis)
	if err != nil {
		return report{}, err
	}
	defer redisClient.Close()
	kafkaClient, err := kgo.NewClient(kgo.SeedBrokers(c.Kafka.Brokers...), kgo.ClientID("v05-readonly-diagnostic"))
	if err != nil {
		return report{}, err
	}
	defer kafkaClient.Close()
	etcdClient, err := clientv3.New(clientv3.Config{Endpoints: c.Etcd.Hosts, DialTimeout: time.Second})
	if err != nil {
		return report{}, err
	}
	defer etcdClient.Close()

	value := report{EtcdEndpoints: map[string][]string{}, StreamPending: map[uint64]int64{}, KafkaLag: map[string]int64{}}
	rows, err := db.QueryContext(ctx, "SELECT id FROM seckill_items ORDER BY id")
	if err != nil {
		return report{}, fmt.Errorf("list stream item IDs: %w", err)
	}
	for rows.Next() {
		var itemID uint64
		if err := rows.Scan(&itemID); err != nil {
			_ = rows.Close()
			return report{}, err
		}
		summary, pendingErr := redisClient.XPending(ctx, redisgate.StreamKey(itemID), c.StreamGroup).Result()
		if pendingErr == nil {
			value.StreamPending[itemID] = summary.Count
		} else if !redis.HasErrorPrefix(pendingErr, "NOGROUP") {
			_ = rows.Close()
			return report{}, fmt.Errorf("inspect Stream PEL item %d: %w", itemID, pendingErr)
		}
	}
	if err := rows.Close(); err != nil {
		return report{}, err
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(SUM(claimed_by IS NOT NULL), 0) FROM order_outbox_events WHERE status = 1").Scan(&value.OutboxBacklog, &value.OutboxClaimed); err != nil {
		return report{}, fmt.Errorf("inspect order outbox: %w", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM seckill_inventory_reservations").Scan(&value.ReservationCount); err != nil {
		return report{}, err
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM orders WHERE order_no LIKE 'T%'").Scan(&value.SeckillOrderCount); err != nil {
		return report{}, err
	}
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM seckill_inventory_reservations r
		LEFT JOIN orders o ON o.order_no = r.order_no WHERE o.id IS NULL
	`).Scan(&value.ReservedWithoutOrder); err != nil {
		return report{}, err
	}
	for _, key := range c.Etcd.ServiceKeys {
		response, getErr := etcdClient.Get(ctx, key, clientv3.WithPrefix())
		if getErr != nil {
			return report{}, fmt.Errorf("inspect etcd key %s: %w", key, getErr)
		}
		endpoints := make([]string, 0, len(response.Kvs))
		for _, pair := range response.Kvs {
			endpoint := strings.TrimSpace(string(pair.Value))
			if endpoint != "" {
				endpoints = append(endpoints, endpoint)
			}
		}
		sort.Strings(endpoints)
		value.EtcdEndpoints[key] = endpoints
	}
	for _, group := range c.Kafka.Groups {
		lag, lagErr := kafkaGroupLag(ctx, kafkaClient, c.Kafka.Topic, group)
		if lagErr != nil {
			return report{}, lagErr
		}
		value.KafkaLag[group] = lag
	}
	// 本命令到此为止：只读诊断不能 XACK、修改 outbox、提交 offset、删除 DLQ 或自动补库存。
	return value, nil
}

func validateConfig(c appConfig) error {
	if err := c.Redis.Validate(); err != nil {
		return err
	}
	if c.MySQL.DataSource == "" || c.StreamGroup == "" || len(c.Kafka.Brokers) == 0 || c.Kafka.Topic == "" || len(c.Kafka.Groups) != 2 || len(c.Etcd.Hosts) == 0 || len(c.Etcd.ServiceKeys) == 0 || c.TimeoutMilliseconds <= 0 {
		return fmt.Errorf("complete MySQL/Redis/Stream/Kafka/etcd diagnostic config is required")
	}
	if c.Kafka.Groups[0] == c.Kafka.Groups[1] {
		return fmt.Errorf("projector and notification Kafka groups must differ")
	}
	return nil
}

func kafkaGroupLag(ctx context.Context, client *kgo.Client, topic, group string) (int64, error) {
	metadataRequest := kmsg.NewPtrMetadataRequest()
	metadataRequest.Topics = []kmsg.MetadataRequestTopic{{Topic: &topic}}
	metadata, err := metadataRequest.RequestWith(ctx, client)
	if err != nil || len(metadata.Topics) != 1 {
		return 0, fmt.Errorf("inspect Kafka metadata: %w", err)
	}
	partitions := make([]int32, 0, len(metadata.Topics[0].Partitions))
	latestRequest := kmsg.NewPtrListOffsetsRequest()
	latestTopic := kmsg.ListOffsetsRequestTopic{Topic: topic}
	for _, partition := range metadata.Topics[0].Partitions {
		partitions = append(partitions, partition.Partition)
		latestTopic.Partitions = append(latestTopic.Partitions, kmsg.ListOffsetsRequestTopicPartition{Partition: partition.Partition, CurrentLeaderEpoch: -1, Timestamp: -1, MaxNumOffsets: 1})
	}
	latestRequest.Topics = []kmsg.ListOffsetsRequestTopic{latestTopic}
	latestResponse, err := latestRequest.RequestWith(ctx, client)
	if err != nil || len(latestResponse.Topics) != 1 {
		return 0, fmt.Errorf("inspect Kafka end offsets: %w", err)
	}
	committedRequest := kmsg.NewPtrOffsetFetchRequest()
	committedRequest.SetVersion(7)
	committedRequest.Group = group
	committedRequest.Topics = []kmsg.OffsetFetchRequestTopic{{Topic: topic, Partitions: partitions}}
	committedResponse, err := committedRequest.RequestWith(ctx, client)
	if err != nil || len(committedResponse.Topics) != 1 {
		return 0, fmt.Errorf("inspect Kafka committed offsets group %s: %w", group, err)
	}
	committed := make(map[int32]int64, len(partitions))
	for _, partition := range committedResponse.Topics[0].Partitions {
		if partition.Offset >= 0 {
			committed[partition.Partition] = partition.Offset
		}
	}
	var lag int64
	for _, partition := range latestResponse.Topics[0].Partitions {
		if partition.Offset > committed[partition.Partition] {
			lag += partition.Offset - committed[partition.Partition]
		}
	}
	return lag, nil
}
