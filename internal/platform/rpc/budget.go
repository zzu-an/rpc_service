// Package rpc 提供所有业务 RPC 共用的治理基础，不包含任何领域逻辑。
package rpc

import (
	"context"
	"errors"
	"time"
)

var ErrInvalidBudget = errors.New("rpc budget must be positive and parent context must not be nil")

// WithBudget 从调用方 context 派生单跳预算，绝不使用 context.Background() 截断取消链。
// 总预算必须先于子预算：即使 max 更长，父 context 的更早 deadline 仍会获胜；这能避免网关已经
// 超时后，下游 goroutine 继续占用连接。面试时可进一步讨论为串行多跳预留清理/编码余量。
func WithBudget(parent context.Context, max time.Duration) (context.Context, context.CancelFunc, error) {
	if parent == nil || max <= 0 {
		return nil, nil, ErrInvalidBudget
	}
	ctx, cancel := context.WithTimeout(parent, max)
	return ctx, cancel, nil
}
