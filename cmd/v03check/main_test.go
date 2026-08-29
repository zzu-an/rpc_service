package main

import (
	"context"
	"errors"
	"testing"
	"time"

	seckillmysql "service_rpc/internal/seckill/mysqlrepo"
	"service_rpc/internal/seckill/redisgate"
)

type mysqlReaderStub struct {
	state seckillmysql.ItemConsistencyState
	err   error
}

func (s mysqlReaderStub) InspectItemState(context.Context, uint64) (seckillmysql.ItemConsistencyState, error) {
	return s.state, s.err
}

type redisReaderStub struct {
	state redisgate.ItemConsistencyState
	err   error
}

func (s redisReaderStub) InspectItem(context.Context, uint64) (redisgate.ItemConsistencyState, error) {
	return s.state, s.err
}

func TestBuildReportClassifiesStates(t *testing.T) {
	tests := []struct {
		name  string
		mysql seckillmysql.ItemConsistencyState
		redis redisgate.ItemConsistencyState
		want  diagnosticStatus
	}{
		{name: "consistent", mysql: seckillmysql.ItemConsistencyState{InitialStock: 10, AvailableStock: 7, ClaimCount: 3}, redis: redisgate.ItemConsistencyState{Exists: true, Stock: 7, BuyerCount: 3, TTL: time.Minute}, want: statusConsistent},
		{name: "reserved ahead", mysql: seckillmysql.ItemConsistencyState{InitialStock: 10, AvailableStock: 8, ClaimCount: 2}, redis: redisgate.ItemConsistencyState{Exists: true, Stock: 6, BuyerCount: 4, TTL: time.Minute}, want: statusReservedAhead},
		{name: "cache missing", mysql: seckillmysql.ItemConsistencyState{InitialStock: 10, AvailableStock: 10}, redis: redisgate.ItemConsistencyState{}, want: statusCacheMissing},
		{name: "redis stock too high", mysql: seckillmysql.ItemConsistencyState{InitialStock: 10, AvailableStock: 5, ClaimCount: 5}, redis: redisgate.ItemConsistencyState{Exists: true, Stock: 6, BuyerCount: 4, TTL: time.Minute}, want: statusDangerous},
		{name: "delta mismatch", mysql: seckillmysql.ItemConsistencyState{InitialStock: 10, AvailableStock: 8, ClaimCount: 2}, redis: redisgate.ItemConsistencyState{Exists: true, Stock: 7, BuyerCount: 4, TTL: time.Minute}, want: statusDangerous},
		{name: "missing ttl", mysql: seckillmysql.ItemConsistencyState{InitialStock: 10, AvailableStock: 10}, redis: redisgate.ItemConsistencyState{Exists: true, Stock: 10, TTL: -1}, want: statusDangerous},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildReport(context.Background(), 1, mysqlReaderStub{state: tt.mysql}, redisReaderStub{state: tt.redis})
			if got.Status != tt.want {
				t.Fatalf("status = %s, want %s; report=%+v", got.Status, tt.want, got)
			}
		})
	}
}

func TestBuildReportUnknownDoesNotTurnErrorsIntoZero(t *testing.T) {
	got := buildReport(context.Background(), 1, mysqlReaderStub{err: errors.New("db down")}, redisReaderStub{})
	if got.Status != statusUnknown || len(got.ErrorCategories) != 1 || got.ErrorCategories[0] != "mysql_read_failed" {
		t.Fatalf("report = %+v", got)
	}
}
