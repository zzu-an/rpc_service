// Package config defines configuration shared by the monolith API and its
// development commands.
package config

import "github.com/zeromicro/go-zero/rest"

// Config is the complete v0.1 process configuration.
type Config struct {
	rest.RestConf // RestConf 内部封装了启动一个 HTTP 服务所需的核心配置：
	MySQL         MySQLConfig
	Auth          AuthConfig
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
