// SPDX-License-Identifier: GPL-3.0-or-later

package transport

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeClock moves forward only when asked to, or when a Sleep is requested.
// It makes the rate verifiable without waiting in real time.
type fakeClock struct {
	mu    sync.Mutex
	now   time.Time
	slept []time.Duration
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(0, 0)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Sleep(_ context.Context, d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.slept = append(c.slept, d)
	c.now = c.now.Add(d)
	return nil
}

func (c *fakeClock) totalSlept() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	var t time.Duration
	for _, d := range c.slept {
		t += d
	}
	return t
}

func TestTokenBucketAllowsBurstWithoutSleeping(t *testing.T) {
	clock := newFakeClock()
	b := NewTokenBucket(5, 10, clock)

	for i := 0; i < 10; i++ {
		if err := b.Wait(context.Background()); err != nil {
			t.Fatalf("Wait() #%d error = %v", i, err)
		}
	}
	if got := clock.totalSlept(); got != 0 {
		t.Errorf("the burst of 10 slept %v, want 0", got)
	}
}

func TestTokenBucketThrottlesBeyondBurst(t *testing.T) {
	clock := newFakeClock()
	b := NewTokenBucket(5, 10, clock)

	for i := 0; i < 15; i++ {
		if err := b.Wait(context.Background()); err != nil {
			t.Fatalf("Wait() #%d error = %v", i, err)
		}
	}
	// 10 in a burst, then 5 at 5/s: exactly 1 second of cumulative sleep. The
	// UPPER bound matters as much as the lower one: --rate is exposed to the
	// user, and a limiter that over-throttles by a factor of 10 would pass a
	// `>=` assertion.
	if got := clock.totalSlept(); got != time.Second {
		t.Errorf("cumulative sleep = %v, want exactly 1s", got)
	}
}

func TestTokenBucketRefillsOverTime(t *testing.T) {
	clock := newFakeClock()
	b := NewTokenBucket(5, 10, clock)

	for i := 0; i < 10; i++ {
		if err := b.Wait(context.Background()); err != nil {
			t.Fatalf("Wait() #%d error = %v", i, err)
		}
	}
	// 2 seconds go by: the bucket refills to 10 (capped at the burst).
	_ = clock.Sleep(context.Background(), 2*time.Second)
	before := clock.totalSlept()
	for i := 0; i < 10; i++ {
		if err := b.Wait(context.Background()); err != nil {
			t.Fatalf("Wait() after refill #%d error = %v", i, err)
		}
	}
	if got := clock.totalSlept() - before; got != 0 {
		t.Errorf("after refill, sleep = %v, want 0", got)
	}
}

func TestTokenBucketRespectsContextCancellation(t *testing.T) {
	clock := newFakeClock()
	b := NewTokenBucket(1, 1, clock)

	if err := b.Wait(context.Background()); err != nil {
		t.Fatalf("first Wait error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.Wait(ctx); err == nil {
		t.Error("Wait on a cancelled context must return an error")
	}
}

// RealClock.Sleep must return as soon as it is cancelled, not at the end of
// the requested duration. Without that, a Ctrl+C during a rate-limited apply
// would wait for the end of the backoff.
func TestRealClockSleepReturnsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	err := RealClock{}.Sleep(ctx, 10*time.Second)
	elapsed := time.Since(start)

	if err == nil {
		t.Error("Sleep() error = nil, want a cancellation error")
	}
	if elapsed > 2*time.Second {
		t.Errorf("Sleep lasted %v: the cancellation did not interrupt the wait", elapsed)
	}
}

// cancellingClock cancels the context as a side effect of its Sleep, then
// returns nil. It targets the ctx re-check Wait does AFTER the Sleep: without
// it, a token would be consumed on an already cancelled context.
type cancellingClock struct {
	now    time.Time
	cancel context.CancelFunc
}

func (c *cancellingClock) Now() time.Time { return c.now }

func (c *cancellingClock) Sleep(_ context.Context, d time.Duration) error {
	c.now = c.now.Add(d)
	c.cancel()
	return nil
}

func TestTokenBucketRechecksContextAfterSleeping(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	clock := &cancellingClock{now: time.Unix(0, 0), cancel: cancel}
	b := NewTokenBucket(1, 1, clock)

	if err := b.Wait(ctx); err != nil {
		t.Fatalf("first Wait error = %v", err)
	}
	// The bucket is empty: this Wait sleeps, and the Sleep cancels the context.
	if err := b.Wait(ctx); err == nil {
		t.Error("Wait() error = nil: the context cancelled during the Sleep was ignored")
	}
}

func TestTokenBucketIsSharedAcrossGoroutines(t *testing.T) {
	clock := newFakeClock()
	b := NewTokenBucket(5, 10, clock)

	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = b.Wait(context.Background())
		}()
	}
	wg.Wait()
	// 30 calls, burst 10, 5/s: 20 tokens to produce, so 4 cumulative seconds.
	// Lower bound: without sharing, each goroutine would have its own burst
	// and nobody would sleep. Upper bound: the sleep is split into one-token
	// slices, but their sum must not exceed the time needed — otherwise --rate
	// no longer means what it says. The one-slice margin covers wake-ups that
	// cross.
	const want = 4 * time.Second
	got := clock.totalSlept()
	if got < want || got > want+time.Second/5 {
		t.Errorf("cumulative sleep = %v, want %v (±1 slice) — rate badly bounded", got, want)
	}
}
