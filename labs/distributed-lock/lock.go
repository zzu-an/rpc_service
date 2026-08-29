package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	redis "github.com/redis/go-redis/v9"
)

const releaseScript = `
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`

const maxLeaseTTL = 30 * time.Second

type Lease struct {
	client *redis.Client
	key    string
	token  string
	ttl    time.Duration
}

// Acquire 使用 SET NX PX 创建带租约的锁。
// token 代表“这一次获取”，不能使用进程 ID、用户 ID 等可复用值；否则旧请求可能
// 删除后来持有者的锁。TTL 只是防死锁的租约，不是业务所有权的永久证明。
func Acquire(ctx context.Context, client *redis.Client, key string, ttl time.Duration) (*Lease, bool, error) {
	if client == nil || key == "" || ttl <= 0 || ttl > maxLeaseTTL {
		return nil, false, errors.New("client, key, and bounded positive TTL are required")
	}
	token, err := newToken()
	if err != nil {
		return nil, false, fmt.Errorf("generate lock token: %w", err)
	}
	ok, err := client.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return nil, false, fmt.Errorf("acquire Redis lease: %w", err)
	}
	if !ok {
		return nil, false, nil
	}
	return &Lease{client: client, key: key, token: token, ttl: ttl}, true, nil
}

// Release 必须把“比较 token”和“删除”放进同一个 Lua。
// 错误写法 GET 后 DEL 中间存在竞态：旧锁过期、新持有者写入后，旧请求仍可能 DEL 掉新锁。
func (l *Lease) Release(ctx context.Context) (bool, error) {
	if l == nil || l.client == nil {
		return false, errors.New("lease is not initialized")
	}
	deleted, err := l.client.Eval(ctx, releaseScript, []string{l.key}, l.token).Int64()
	if err != nil {
		return false, fmt.Errorf("release Redis lease: %w", err)
	}
	return deleted == 1, nil
}

func newToken() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
