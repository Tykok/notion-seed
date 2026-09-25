// SPDX-License-Identifier: GPL-3.0-or-later

package transport

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// DefaultRatePerSec and DefaultBurst: the spike measured 21 req/s for reads
// and 7.5 writes/s, without a single 429 and without a single Retry-After
// header. The ~3 req/s cap announced by the documentation was never reached.
// Wiring the 2.5 req/s that cap suggests would throttle every apply by a
// factor of 8 on an unverified basis; 5 keeps a margin while staying measured.
const (
	DefaultRatePerSec = 5.0
	DefaultBurst      = 10
)

// Clock isolates time to make the rate testable without waiting.
type Clock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) error
}

type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

func (RealClock) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Limiter bounds the call rate. The instance is shared by all workers: one
// limiter per worker bounds nothing.
type Limiter interface {
	Wait(ctx context.Context) error
}

// TokenBucket is a classic token bucket, safe for concurrent use.
type TokenBucket struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
	clock  Clock
}

// NewTokenBucket panics if ratePerSec or burst is not strictly positive. It is
// a programming contract, like regexp.MustCompile: a zero rate would sleep for
// 292 years (time.Duration(+Inf) saturates at MaxInt64) and a negative rate
// would make Wait spin idle, burning a core. Callers that read a user value
// validate it before getting here.
func NewTokenBucket(ratePerSec float64, burst int, clock Clock) *TokenBucket {
	if ratePerSec <= 0 {
		panic(fmt.Sprintf("NewTokenBucket: ratePerSec must be > 0, got %v", ratePerSec))
	}
	if burst <= 0 {
		panic(fmt.Sprintf("NewTokenBucket: burst must be > 0, got %d", burst))
	}
	if clock == nil {
		clock = RealClock{}
	}
	return &TokenBucket{
		rate:   ratePerSec,
		burst:  float64(burst),
		tokens: float64(burst),
		last:   clock.Now(),
		clock:  clock,
	}
}

func (b *TokenBucket) Wait(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for {
		b.mu.Lock()
		now := b.clock.Now()
		b.tokens += now.Sub(b.last).Seconds() * b.rate
		if b.tokens > b.burst {
			b.tokens = b.burst
		}
		b.last = now

		if b.tokens >= 1 {
			b.tokens--
			b.mu.Unlock()
			return nil
		}
		need := time.Duration((1 - b.tokens) / b.rate * float64(time.Second))
		b.mu.Unlock()

		if err := b.clock.Sleep(ctx, need); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}
