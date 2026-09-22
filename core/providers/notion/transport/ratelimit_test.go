// SPDX-License-Identifier: GPL-3.0-or-later

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
	// 10 en burst, puis 5 à 5/s : exactement 1 seconde de sommeil cumulé. La
	// borne SUPÉRIEURE compte autant que l'inférieure : --rate est exposé à
	// l'utilisateur, et un limiteur qui sur-étrangle d'un facteur 10 passerait
	// une assertion en `>=`.
	if got := clock.totalSlept(); got != time.Second {
		t.Errorf("sommeil cumulé = %v, want exactement 1s", got)
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

// RealClock.Sleep doit rendre dès l'annulation, pas au bout de la durée
// demandée. Sans ça, un Ctrl+C pendant un apply rate-limité attendrait la fin
// du backoff.
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
		t.Error("Sleep() error = nil, want une erreur d'annulation")
	}
	if elapsed > 2*time.Second {
		t.Errorf("Sleep a duré %v : l'annulation n'a pas interrompu l'attente", elapsed)
	}
}

// cancellingClock annule le contexte en effet de bord de son Sleep, puis rend
// nil. Ça cible la revérification de ctx que Wait fait APRÈS le Sleep : sans
// elle, un jeton serait consommé sur un contexte déjà annulé.
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
		t.Fatalf("premier Wait error = %v", err)
	}
	// Le bucket est vide : ce Wait dort, et le Sleep annule le contexte.
	if err := b.Wait(ctx); err == nil {
		t.Error("Wait() error = nil : le contexte annulé pendant le Sleep a été ignoré")
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
	// 30 appels, burst 10, 5/s : 20 jetons à produire, donc 4 secondes cumulées.
	// Borne inférieure : sans partage, chaque goroutine aurait son propre burst
	// et personne ne dormirait. Borne supérieure : le sommeil est découpé en
	// tranches d'un jeton, mais leur somme ne doit pas dépasser le temps
	// nécessaire — sinon --rate ne veut plus dire ce qu'il annonce. La marge
	// d'une tranche couvre les réveils qui se croisent.
	const want = 4 * time.Second
	got := clock.totalSlept()
	if got < want || got > want+time.Second/5 {
		t.Errorf("sommeil cumulé = %v, want %v (±1 tranche) — débit mal borné", got, want)
	}
}
