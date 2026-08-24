package main

import "testing"

func TestSafeCounters(t *testing.T) {
	const want = 80_000
	for name, value := range map[string]int64{
		"mutex":  incrementConcurrently(&MutexCounter{}, 8, 10_000),
		"atomic": incrementConcurrently(&AtomicCounter{}, 8, 10_000),
	} {
		if value != want {
			t.Errorf("%s=%d, want %d", name, value, want)
		}
	}
}

func TestMutexMaintainsCompoundInvariant(t *testing.T) {
	counter := &MutexCounter{}
	const limit = 100
	done := make(chan struct{}, 200)
	for i := 0; i < cap(done); i++ {
		go func() {
			counter.AddIfBelow(limit)
			done <- struct{}{}
		}()
	}
	for i := 0; i < cap(done); i++ {
		<-done
	}
	if got := counter.Value(); got != limit {
		t.Fatalf("value=%d, want %d", got, limit)
	}
}

func BenchmarkMutexCounter(b *testing.B) {
	counter := &MutexCounter{}
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			counter.Inc()
		}
	})
}

func BenchmarkAtomicCounter(b *testing.B) {
	counter := &AtomicCounter{}
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			counter.Inc()
		}
	})
}
