package transport

import (
	"context"
	"sync"
	"time"
)

// DefaultRatePerSec et DefaultBurst : le spike a mesuré 21 req/s en lecture et
// 7,5 writes/s sans un seul 429. Le plafond de ~3 req/s annoncé par la
// documentation n'a jamais été atteint. 5 garde de la marge sans brider apply
// d'un facteur 8 sur une base non vérifiée.
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
	mu       sync.Mutex
	rate     float64
	burst    float64
	tokens   float64
	last     time.Time
	clock    Clock
}

func NewTokenBucket(ratePerSec float64, burst int, clock Clock) *TokenBucket {
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
