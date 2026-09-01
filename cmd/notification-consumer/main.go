// Command notification-consumer is an independently scalable Kafka consumer group member.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os/signal"
	"syscall"
	"time"

	"service_rpc/internal/config"
	"service_rpc/internal/notification"
	notificationmysql "service_rpc/internal/notification/mysqlrepo"
	"service_rpc/internal/platform/database"
	"service_rpc/internal/platform/mq"

	"github.com/zeromicro/go-zero/core/conf"
)

var configFile = flag.String("f", "etc/notification-consumer.yaml", "notification consumer config file")

type appConfig struct {
	MySQL config.MySQLConfig
	Kafka struct {
		Brokers                      []string
		ClientID                     string
		GroupID                      string
		Topics                       []string
		RetryTopic                   string
		DLQTopic                     string
		MaxAttempts                  int
		OperationTimeoutMilliseconds int
	}
}

func main() {
	flag.Parse()
	if err := run(*configFile); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}

func run(path string) error {
	var c appConfig
	if err := conf.Load(path, &c); err != nil {
		return fmt.Errorf("load notification consumer config: %w", err)
	}
	timeout := time.Duration(c.Kafka.OperationTimeoutMilliseconds) * time.Millisecond
	kafkaConfig := mq.KafkaConfig{Brokers: c.Kafka.Brokers, ClientID: c.Kafka.ClientID, GroupID: c.Kafka.GroupID, Topics: c.Kafka.Topics, OperationTimeout: timeout}
	source, err := mq.NewGroupClient(kafkaConfig)
	if err != nil {
		return err
	}
	defer source.Close()
	publisher, err := mq.NewPublisher(mq.KafkaConfig{Brokers: c.Kafka.Brokers, ClientID: c.Kafka.ClientID + "-retry", OperationTimeout: timeout})
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
		return err
	}
	defer db.Close()
	if err := source.Ping(startupContext); err != nil {
		cancel()
		return fmt.Errorf("initialize notification Kafka: %w", err)
	}
	cancel()
	service, err := notification.NewService(notificationmysql.New(db))
	if err != nil {
		return err
	}
	consumer, err := mq.NewConsumer(source, publisher, notification.NewOrderCreatedHandler(service), mq.ConsumerConfig{
		RetryTopic: c.Kafka.RetryTopic, DLQTopic: c.Kafka.DLQTopic, MaxAttempts: c.Kafka.MaxAttempts,
	})
	if err != nil {
		return err
	}
	// 同 group 的两个实例分摊 partition；扩容数大于 partition 数时会有空闲实例，这是 Kafka
	// 面试中常见的“为什么加 consumer 仍不提速”。projector 必须使用另一个 group 才能 fan-out。
	log.Printf("starting notification consumer group=%s topics=%v", c.Kafka.GroupID, c.Kafka.Topics)
	return consumer.Run(ctx)
}
