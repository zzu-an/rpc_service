// Package mq provides the Kafka runtime boundary shared by relay and consumers.
package mq

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const AttemptHeader = "x-attempt"

var ErrPermanent = errors.New("permanent message error")

type Message struct {
	Topic     string
	Key       []byte
	Value     []byte
	Headers   map[string][]byte
	Partition int32
	Offset    int64
}

func (m Message) Attempt() (int, error) {
	value := strings.TrimSpace(string(m.Headers[AttemptHeader]))
	if value == "" {
		return 0, nil
	}
	attempt, err := strconv.Atoi(value)
	if err != nil || attempt < 0 {
		return 0, fmt.Errorf("invalid Kafka attempt header: %w", ErrPermanent)
	}
	return attempt, nil
}

func (m Message) WithAttempt(topic string, attempt int) Message {
	headers := make(map[string][]byte, len(m.Headers)+1)
	for key, value := range m.Headers {
		headers[key] = append([]byte(nil), value...)
	}
	headers[AttemptHeader] = []byte(strconv.Itoa(attempt))
	return Message{Topic: topic, Key: append([]byte(nil), m.Key...), Value: append([]byte(nil), m.Value...), Headers: headers}
}

type Publisher interface {
	Publish(ctx context.Context, message Message) error
}

type Source interface {
	Fetch(ctx context.Context) (Message, error)
	Commit(ctx context.Context, message Message) error
}

type Handler func(ctx context.Context, message Message) error

func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %v", ErrPermanent, err)
}
