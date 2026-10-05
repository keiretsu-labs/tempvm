package gateway

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionLimiterEnforcesConcurrentCap(t *testing.T) {
	const (
		limit      = 3
		contenders = 64
	)
	limiter := newSessionLimiter(limit)
	start := make(chan struct{})
	release := make(chan struct{})
	var acquired atomic.Int64
	var wg sync.WaitGroup

	for range contenders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if !limiter.TryAcquire() {
				return
			}
			acquired.Add(1)
			<-release
			limiter.Release()
		}()
	}
	close(start)

	deadline := time.Now().Add(2 * time.Second)
	for acquired.Load() < limit && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := acquired.Load(); got != limit {
		close(release)
		wg.Wait()
		t.Fatalf("acquired = %d, want %d", got, limit)
	}
	active, max := limiter.Snapshot()
	if active != limit || max != limit {
		close(release)
		wg.Wait()
		t.Fatalf("Snapshot() = (%d, %d), want (%d, %d)", active, max, limit, limit)
	}

	close(release)
	wg.Wait()
	active, _ = limiter.Snapshot()
	if active != 0 {
		t.Fatalf("active after release = %d, want 0", active)
	}
}

func TestSessionLimiterCanBeUnlimited(t *testing.T) {
	limiter := newSessionLimiter(0)
	for range 10 {
		if !limiter.TryAcquire() {
			t.Fatal("unlimited limiter refused a session")
		}
	}
	active, max := limiter.Snapshot()
	if active != 10 || max != 0 {
		t.Fatalf("Snapshot() = (%d, %d), want (10, 0)", active, max)
	}
}

func TestSessionMetadataRoundTrip(t *testing.T) {
	startedAt := time.Date(2026, time.October, 5, 12, 0, 0, 0, time.UTC)
	ctx := withSessionMetadata(context.Background(), "tempvm-test", startedAt)
	metadata := sessionMetadataFrom(ctx)
	if metadata.Name != "tempvm-test" || !metadata.StartedAt.Equal(startedAt) {
		t.Fatalf("session metadata = %#v", metadata)
	}
}
