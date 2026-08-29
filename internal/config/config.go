// Package config defines configuration shared by the monolith API and its
// development commands.
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/zeromicro/go-zero/rest"
)

// Config is the complete v0.2 process configuration.
type Config struct {
	rest.RestConf // RestConf 内部封装了启动一个 HTTP 服务所需的核心配置：
	MySQL         MySQLConfig
	Redis         RedisConfig
	Auth          AuthConfig
	Seckill       SeckillConfig
}

// MySQLConfig contains connection and conservative pool settings. These pool
// sizes are operational safeguards, not claimed performance optimizations;
// later tuning requires measurements from the real workload.
type MySQLConfig struct {
	DataSource             string
	MaxOpenConns           int
	MaxIdleConns           int
	ConnMaxLifetimeSeconds int
}

// AuthConfig configures short-lived access tokens. The local secret is only a
// development value; deployed environments must inject a separate secret.
type AuthConfig struct {
	AccessSecret     string
	AccessTTLSeconds int
}

// SeckillConfig 只在进程启动时选择库存实现，绝不能由单个 HTTP 请求动态指定。
// 压测三种策略时应修改配置并重启服务，保证一次测试期间所有请求使用相同策略。
type SeckillConfig struct {
	StockMode     string
	AdmissionMode string
}

// RedisConfig 把连接建立预算和单次命令预算分开。
//
// 面试常问“Redis 超时配多大”：它不能脱离 HTTP 总超时单独决定。若一次 Redis
// 操作就耗尽整个请求预算，后续 MySQL 即使健康也没有完成时间，还会造成 goroutine
// 和连接在故障期间堆积。因此这里要求两类超时都显式、有限且可验证。
type RedisConfig struct {
	Address                      string
	Username                     string
	Password                     string
	DB                           int
	DialTimeoutMilliseconds      int
	OperationTimeoutMilliseconds int
}

func (c RedisConfig) DialTimeout() time.Duration {
	return time.Duration(c.DialTimeoutMilliseconds) * time.Millisecond
}

func (c RedisConfig) OperationTimeout() time.Duration {
	return time.Duration(c.OperationTimeoutMilliseconds) * time.Millisecond
}

func (c RedisConfig) Validate() error {
	if strings.TrimSpace(c.Address) == "" {
		return fmt.Errorf("redis address is required")
	}
	if c.DB < 0 {
		return fmt.Errorf("redis DB must not be negative")
	}
	if c.DialTimeoutMilliseconds <= 0 {
		return fmt.Errorf("redis dial timeout must be positive")
	}
	if c.OperationTimeoutMilliseconds <= 0 {
		return fmt.Errorf("redis operation timeout must be positive")
	}
	return nil
}

type AdmissionMode uint8

const (
	AdmissionModeMySQL AdmissionMode = iota + 1
	AdmissionModeRedis
)

// ParseAdmissionMode 只允许进程启动时选择准入路径。
// Redis 故障时静默切回 MySQL 会把原本被缓存拦住的峰值瞬间压回热点库存行，
// 很容易把单点缓存故障放大成数据库雪崩；因此运行期绝不能自动降级为直连数据库。
func ParseAdmissionMode(value string) (AdmissionMode, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "mysql":
		return AdmissionModeMySQL, nil
	case "redis":
		return AdmissionModeRedis, nil
	default:
		return 0, fmt.Errorf("unsupported seckill admission mode %q; use mysql or redis", value)
	}
}

func (m AdmissionMode) String() string {
	switch m {
	case AdmissionModeMySQL:
		return "mysql"
	case AdmissionModeRedis:
		return "redis"
	default:
		return fmt.Sprintf("unknown(%d)", m)
	}
}
