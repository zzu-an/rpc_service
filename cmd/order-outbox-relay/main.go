package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os/signal"
	"syscall"
	"time"

	"service_rpc/internal/config"
	ordermysql "service_rpc/internal/order/mysqlrepo"
	"service_rpc/internal/order/outboxrelay"
	"service_rpc/internal/platform/database"
	"service_rpc/internal/platform/mq"

	"github.com/zeromicro/go-zero/core/conf"
)

var configFile = flag.String("f", "etc/order-outbox-relay.yaml", "order outbox relay config file")

type relayConfig struct {
	WorkerID              string
	Topic                 string
	BatchSize             int
	LeaseMilliseconds     int
	PollMilliseconds      int
	RetryBaseMilliseconds int
	RetryMaxMilliseconds  int
}

type appConfig struct {
	MySQL config.MySQLConfig
	Kafka struct {
		Brokers                      []string
		ClientID                     string
		OperationTimeoutMilliseconds int
	}
	Relay relayConfig
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
		return fmt.Errorf("load order outbox relay config: %w", err)
	}
	operationTimeout := time.Duration(c.Kafka.OperationTimeoutMilliseconds) * time.Millisecond
	publisher, err := mq.NewPublisher(mq.KafkaConfig{Brokers: c.Kafka.Brokers, ClientID: c.Kafka.ClientID, OperationTimeout: operationTimeout})
	if err != nil {
		return err
	}
	defer publisher.Close()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	startupContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	db, err := database.OpenMySQL(startupContext, c.MySQL)
	if err != nil {
		cancel()
		return fmt.Errorf("initialize outbox MySQL: %w", err)
	}
	defer db.Close()
	if err := publisher.Ping(startupContext); err != nil {
		// relay 可在 Kafka 恢复后由 supervisor 重启；order-rpc 本身不依赖这一启动结果。
		cancel()
		return fmt.Errorf("initialize outbox Kafka: %w", err)
	}
	cancel()
	relay, err := outboxrelay.New(ordermysql.New(db), publisher, outboxrelay.Config{
		WorkerID: c.Relay.WorkerID, Topic: c.Relay.Topic, BatchSize: c.Relay.BatchSize,
		Lease:     time.Duration(c.Relay.LeaseMilliseconds) * time.Millisecond,
		Poll:      time.Duration(c.Relay.PollMilliseconds) * time.Millisecond,
		RetryBase: time.Duration(c.Relay.RetryBaseMilliseconds) * time.Millisecond,
		RetryMax:  time.Duration(c.Relay.RetryMaxMilliseconds) * time.Millisecond,
	})
	if err != nil {
		return err
	}
	log.Printf("starting order outbox relay worker=%s topic=%s", c.Relay.WorkerID, c.Relay.Topic)
	return relay.Run(ctx)
}
