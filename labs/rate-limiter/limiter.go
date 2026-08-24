package main

import (
	"sync"
	"time"
)

// Limiter is a concurrency-safe token bucket. rate is tokens per second and
// burst is both the bucket capacity and maximum single-request token count.
type Limiter struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

func New(rate float64, burst int, now time.Time) *Limiter {
	if rate <= 0 || burst <= 0 {
		panic("rate and burst must be positive")
	}
	return &Limiter{rate: rate, burst: float64(burst), tokens: float64(burst), last: now}
}

func (l *Limiter) Allow(now time.Time) bool {
	return l.AllowN(now, 1)
}

func (l *Limiter) AllowN(now time.Time, n int) bool {
	if n <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if now.Before(l.last) {
		now = l.last
	}
	elapsed := now.Sub(l.last).Seconds()
	l.tokens = min(l.burst, l.tokens+elapsed*l.rate)
	l.last = now
	if float64(n) > l.burst || l.tokens < float64(n) {
		return false
	}
	l.tokens -= float64(n)
	return true
}
