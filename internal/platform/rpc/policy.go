package rpc

import (
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type PolicyConfig struct {
	MaxAttempts       int
	BackoffBase       time.Duration
	BackoffMax        time.Duration
	JitterRatio       float64
	BreakerFailures   int
	BreakerOpenPeriod time.Duration
}

func (c PolicyConfig) Validate() error {
	if c.MaxAttempts <= 0 || c.BackoffBase <= 0 || c.BackoffMax < c.BackoffBase || c.JitterRatio < 0 || c.JitterRatio > 1 || c.BreakerFailures <= 0 || c.BreakerOpenPeriod <= 0 {
		return fmt.Errorf("invalid RPC retry/breaker policy")
	}
	return nil
}

// Policy 把三种治理职责分开：调用方 deadline 限制单次/总耗时；retry 只恢复短暂抖动；
// breaker 在连续依赖故障后快速失败。三者都不能提供业务幂等，也不能替代数据库唯一键。
type Policy struct {
	config  PolicyConfig
	breaker breaker
	now     func() time.Time
}

type breaker struct {
	mu                  sync.Mutex
	consecutiveFailures int
	openedAt            time.Time
	halfOpenInFlight    bool
}

func NewPolicy(config PolicyConfig) (*Policy, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &Policy{config: config, now: time.Now}, nil
}

// Do 只有显式声明 idempotent 的调用才可能重试。普通下单/注册即使收到 Unavailable，
// 也不能仅凭 transport error 推断服务端未提交；稳定 order_no + 载荷冲突校验是重试写 RPC 的前提。
func (p *Policy) Do(ctx context.Context, idempotent bool, call func(context.Context) error) error {
	if p == nil || ctx == nil || call == nil {
		return fmt.Errorf("RPC policy, context, and call are required")
	}
	maxAttempts := p.config.MaxAttempts
	if !idempotent {
		maxAttempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !p.breaker.allow(p.now(), p.config.BreakerOpenPeriod) {
			return status.Error(codes.Unavailable, "rpc circuit open")
		}
		lastErr = call(ctx)
		class := Classify(lastErr)
		p.breaker.record(class, p.now(), p.config.BreakerFailures)
		if lastErr == nil || !retryableClass(class) || attempt == maxAttempts {
			return lastErr
		}
		if err := waitPolicy(ctx, p.backoff(attempt)); err != nil {
			return err
		}
	}
	return lastErr
}

func retryableClass(class FailureClass) bool {
	return class == FailureClassDependencyTimeout || class == FailureClassDependencyUnavailable
}

func (p *Policy) backoff(attempt int) time.Duration {
	delay := p.config.BackoffBase
	for step := 1; step < attempt && delay < p.config.BackoffMax; step++ {
		delay *= 2
		if delay > p.config.BackoffMax {
			delay = p.config.BackoffMax
		}
	}
	if p.config.JitterRatio == 0 {
		return delay
	}
	// jitter 打散同一故障后的同步重试；它不是额外预算，等待仍受父 context 截断。
	span := float64(delay) * p.config.JitterRatio
	fraction := float64(p.now().UnixNano()%10_000) / 10_000
	return time.Duration(float64(delay) - span + 2*span*fraction)
}

func (b *breaker) allow(now time.Time, openPeriod time.Duration) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.openedAt.IsZero() {
		return true
	}
	if now.Sub(b.openedAt) < openPeriod || b.halfOpenInFlight {
		return false
	}
	// open 周期后只放一个探针，避免依赖刚恢复就被全部请求同时击穿。
	b.halfOpenInFlight = true
	return true
}

func (b *breaker) record(class FailureClass, now time.Time, threshold int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if retryableClass(class) {
		b.halfOpenInFlight = false
		b.consecutiveFailures++
		if b.consecutiveFailures >= threshold {
			b.openedAt = now
		}
		return
	}
	if class == FailureClassUnspecified || class == FailureClassBusiness {
		// 售罄/参数错误说明依赖正常返回，只是业务拒绝，必须闭合 breaker。
		b.consecutiveFailures = 0
		b.openedAt = time.Time{}
		b.halfOpenInFlight = false
	}
}

func waitPolicy(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
