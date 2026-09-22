// SPDX-License-Identifier: GPL-3.0-or-later

package transport

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// DefaultRatePerSec et DefaultBurst : le spike a mesuré 21 req/s en lecture et
// 7,5 writes/s, sans un seul 429 et sans un seul header Retry-After. Le plafond
// de ~3 req/s annoncé par la documentation n'a jamais été atteint. Câbler les
// 2,5 req/s que ce plafond suggère briderait chaque apply d'un facteur 8 sur
// une base non vérifiée ; 5 garde de la marge tout en restant mesuré.
const (
	DefaultRatePerSec = 5.0
	DefaultBurst      = 10
)

// Clock isole le temps pour rendre le débit testable sans attendre.
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

// Limiter borne le débit d'appels. L'instance est partagée par tous les
// workers : un limiter par worker ne borne rien.
type Limiter interface {
	Wait(ctx context.Context) error
}

// TokenBucket est un seau à jetons classique, sûr en concurrence.
type TokenBucket struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
	clock  Clock
}

// NewTokenBucket panique si ratePerSec ou burst n'est pas strictement positif.
// C'est un contrat de programmation, à la manière de regexp.MustCompile : un
// rate nul ferait dormir 292 ans (time.Duration(+Inf) sature à MaxInt64) et un
// rate négatif ferait tourner Wait à vide en consommant un cœur. Les appelants
// qui lisent une valeur utilisateur la valident avant d'arriver ici.
func NewTokenBucket(ratePerSec float64, burst int, clock Clock) *TokenBucket {
	if ratePerSec <= 0 {
		panic(fmt.Sprintf("NewTokenBucket: ratePerSec doit être > 0, reçu %v", ratePerSec))
	}
	if burst <= 0 {
		panic(fmt.Sprintf("NewTokenBucket: burst doit être > 0, reçu %d", burst))
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
