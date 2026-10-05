package gateway

import (
	"context"
	"sync/atomic"
	"time"
)

type sessionMetadata struct {
	Name      string
	StartedAt time.Time
}

type sessionMetadataKey struct{}

func withSessionMetadata(ctx context.Context, name string, startedAt time.Time) context.Context {
	return context.WithValue(ctx, sessionMetadataKey{}, sessionMetadata{
		Name:      name,
		StartedAt: startedAt,
	})
}

func sessionMetadataFrom(ctx context.Context) sessionMetadata {
	metadata, _ := ctx.Value(sessionMetadataKey{}).(sessionMetadata)
	return metadata
}

type sessionLimiter struct {
	max    int64
	active atomic.Int64
}

func newSessionLimiter(max int) *sessionLimiter {
	return &sessionLimiter{max: int64(max)}
}

func (l *sessionLimiter) TryAcquire() bool {
	if l.max <= 0 {
		l.active.Add(1)
		return true
	}
	for {
		current := l.active.Load()
		if current >= l.max {
			return false
		}
		if l.active.CompareAndSwap(current, current+1) {
			return true
		}
	}
}

func (l *sessionLimiter) Release() {
	for {
		current := l.active.Load()
		if current == 0 {
			return
		}
		if l.active.CompareAndSwap(current, current-1) {
			return
		}
	}
}

func (l *sessionLimiter) Snapshot() (active, max int) {
	return int(l.active.Load()), int(l.max)
}

type Status struct {
	ActiveSessions int
	MaxSessions    int
	MaxSessionTTL  time.Duration
}

type StatusProvider interface {
	Status() Status
}
