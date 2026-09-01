package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/zeromicro/go-zero/zrpc"
)

// RPCServerConfig 只描述一个 RPC 进程自身的监听和注册信息。具体服务后续按数据所有权
// 组合自己的 MySQL/Redis 配置，不能为了“方便”把全系统连接信息塞进共享大配置。
type RPCServerConfig struct {
	zrpc.RpcServerConf
}

// RPCClientConfig 强制使用 etcd 服务发现。Endpoints/Target 虽是 go-zero 支持的能力，
// v0.5 禁止自动回退静态地址：否则 etcd 故障时会绕过治理面，形成难以解释的两套路由语义。
type RPCClientConfig struct {
	zrpc.RpcClientConf
}

func (c RPCServerConfig) Validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("rpc server name is required")
	}
	if err := validateListenOn(c.ListenOn); err != nil {
		return err
	}
	if err := validateEtcd(c.Etcd.Hosts, c.Etcd.Key); err != nil {
		return err
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("rpc server timeout must be positive")
	}
	return nil
}

func (c RPCClientConfig) Validate() error {
	if len(c.Endpoints) != 0 || strings.TrimSpace(c.Target) != "" {
		return fmt.Errorf("rpc client static endpoints and target are disabled; use etcd discovery")
	}
	if err := validateEtcd(c.Etcd.Hosts, c.Etcd.Key); err != nil {
		return err
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("rpc client timeout must be positive")
	}
	return nil
}

func validateEtcd(hosts []string, key string) error {
	if len(hosts) == 0 {
		return fmt.Errorf("etcd hosts are required")
	}
	for _, host := range hosts {
		if strings.TrimSpace(host) == "" {
			return fmt.Errorf("etcd host must not be empty")
		}
	}
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("etcd service key is required")
	}
	return nil
}

func validateListenOn(value string) error {
	host, portText, err := net.SplitHostPort(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("rpc listen address must be host:port: %w", err)
	}
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("rpc listen host is required")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return fmt.Errorf("rpc listen port must be between 1 and 65535")
	}
	return nil
}

// SafeSummary 只提供启动诊断需要的非敏感字段。不要直接使用 %+v 打印完整 zRPC 配置，
// 因为 Etcd.User/Pass 和未来的 RPC token 都可能被带进日志。
func (c RPCServerConfig) SafeSummary() string {
	return fmt.Sprintf("name=%s listen=%s etcd_key=%s etcd_hosts=%d timeout_ms=%d",
		c.Name, c.ListenOn, c.Etcd.Key, len(c.Etcd.Hosts), c.Timeout)
}

func (c RPCClientConfig) SafeSummary() string {
	return fmt.Sprintf("etcd_key=%s etcd_hosts=%d timeout_ms=%d balancer=%s",
		c.Etcd.Key, len(c.Etcd.Hosts), c.Timeout, c.BalancerName)
}
