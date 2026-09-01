package rpc

import (
	"context"
	"fmt"

	"service_rpc/internal/config"

	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
)

type RegisterFunc func(*grpc.Server)

// NewServer 在创建 zRPC server 前执行项目级严格校验。go-zero 原生允许无 etcd 的直连 server，
// 但 v0.5 要验证真实服务发现，因此这里 fail fast，不让不同服务形成两套启动语义。
func NewServer(c config.RPCServerConfig, register RegisterFunc) (*zrpc.RpcServer, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return zrpc.NewServer(c.RpcServerConf, func(server *grpc.Server) {
		register(server)
	})
}

// NewClient 只把 etcd 服务 key 交给 zRPC。endpoint 监听、实例变化和 p2c 负载均衡发生在
// resolver/gRPC client 内部，而不是业务代码里手写轮询。熔断仍不能替代 timeout：熔断在积累
// 失败样本后才打开，单次慢调用必须先由 deadline 有界结束。
func NewClient(ctx context.Context, c config.RPCClientConfig, options ...zrpc.ClientOption) (zrpc.Client, error) {
	if ctx == nil {
		return nil, fmt.Errorf("rpc client startup context is required")
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	type result struct {
		client zrpc.Client
		err    error
	}
	resultChannel := make(chan result, 1)
	go func() {
		client, err := zrpc.NewClient(c.RpcClientConf, options...)
		resultChannel <- result{client: client, err: err}
	}()
	select {
	case completed := <-resultChannel:
		return completed.client, completed.err
	case <-ctx.Done():
		// go-zero v1.10.3 的 resolver 初始化内部使用固定拨号 context，无法注入父 context。
		// 这里先让启动流程按项目预算返回，再等待迟到结果并关闭连接，避免永久泄漏 goroutine/FD。
		go func() {
			completed := <-resultChannel
			if completed.client != nil && completed.client.Conn() != nil {
				_ = completed.client.Conn().Close()
			}
		}()
		return nil, fmt.Errorf("rpc client discovery: %w", ctx.Err())
	}
}
