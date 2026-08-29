// Package cache 提供基础设施级 Redis 客户端生命周期管理。
// 业务 key、Lua 和秒杀语义必须留在 seckill/redisgate，避免 platform 层反向依赖领域层。
package cache

import (
	"context"
	"fmt"

	redis "github.com/redis/go-redis/v9"

	"service_rpc/internal/config"
)

// OpenRedis 创建客户端并在返回前执行 PING。
//
// 这里直接使用 go-redis 而没有使用 go-zero 的便捷 Redis 封装，是因为当前版本的
// 封装不暴露 Close，也不支持选择逻辑 DB。连接是进程级资源，若不能显式释放，测试
// 和滚动重启会留下难以解释的连接；这个取舍只属于基础设施层，不泄漏到业务接口。
func OpenRedis(ctx context.Context, cfg config.RedisConfig) (*redis.Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	client := redis.NewClient(&redis.Options{
		Addr:         cfg.Address,
		Username:     cfg.Username,
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  cfg.DialTimeout(),
		ReadTimeout:  cfg.OperationTimeout(),
		WriteTimeout: cfg.OperationTimeout(),
		PoolTimeout:  cfg.OperationTimeout(),
	})

	// PING 使用独立的命令预算，而不是无界继承 Background。启动失败应快速暴露；
	// 若这里静默成功，首个业务请求才发现 Redis 不通，会把配置错误伪装成线上波动。
	pingCtx, cancel := context.WithTimeout(ctx, cfg.OperationTimeout())
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		// go-redis 的连接错误不包含密码，但这里仍不格式化完整 cfg，防止以后新增字段时泄密。
		return nil, fmt.Errorf("connect redis at %s: %w", cfg.Address, err)
	}
	return client, nil
}
