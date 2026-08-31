package transport

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeClock avance uniquement quand on le lui demande, ou quand un Sleep est
// réclamé. Ça rend le débit vérifiable sans attendre en temps réel.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	slept  []time.Duration
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
		t.Errorf("le burst de 10 a dormi %v, want 0", got)
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
	// 10 en burst, puis 5 à 5/s : au moins 1 seconde de sommeil cumulé.
	if got := clock.totalSlept(); got < time.Second {
		t.Errorf("sommeil cumulé = %v, want >= 1s", got)
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
	// 2 secondes s'écoulent : le bucket se remplit à 10 (plafonné au burst).
	_ = clock.Sleep(context.Background(), 2*time.Second)
	before := clock.totalSlept()
	for i := 0; i < 10; i++ {
		if err := b.Wait(context.Background()); err != nil {
			t.Fatalf("Wait() après recharge #%d error = %v", i, err)
		}
	}
	if got := clock.totalSlept() - before; got != 0 {
		t.Errorf("après recharge, sommeil = %v, want 0", got)
	}
}

func TestTokenBucketRespectsContextCancellation(t *testing.T) {
	clock := newFakeClock()
	b := NewTokenBucket(1, 1, clock)

	if err := b.Wait(context.Background()); err != nil {
		t.Fatalf("premier Wait error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.Wait(ctx); err == nil {
		t.Error("Wait sur un contexte annulé doit retourner une erreur")
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
	// 30 appels, burst 10, 5/s : au moins 4 secondes cumulées.
	if got := clock.totalSlept(); got < 4*time.Second {
		t.Errorf("sommeil cumulé = %v, want >= 4s — le bucket n'est pas partagé", got)
	}
}
