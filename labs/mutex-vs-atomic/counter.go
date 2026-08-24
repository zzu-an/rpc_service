package main

import (
	"sync"
	"sync/atomic"
)

type MutexCounter struct {
	mu    sync.Mutex
	value int64
}

func (c *MutexCounter) Inc() {
	c.mu.Lock()
	c.value++
	c.mu.Unlock()
}

func (c *MutexCounter) Value() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

// AddIfBelow demonstrates a multi-step invariant that is naturally expressed
// under a mutex: check and update must be one critical section.
func (c *MutexCounter) AddIfBelow(limit int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.value >= limit {
		return false
	}
	c.value++
	return true
}

type AtomicCounter struct{ value atomic.Int64 }

func (c *AtomicCounter) Inc()         { c.value.Add(1) }
func (c *AtomicCounter) Value() int64 { return c.value.Load() }

// UnsafeCounter intentionally contains a data race. It is only invoked by the
// standalone demo when -unsafe is supplied, never by the normal test suite.
type UnsafeCounter struct{ value int64 }

func (c *UnsafeCounter) Inc()         { c.value++ }
func (c *UnsafeCounter) Value() int64 { return c.value }
