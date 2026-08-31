package transport

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"time"
)

// RetryPolicy borne les tentatives. Seuls 429 et 5xx sont rejoués.
type RetryPolicy struct {
	MaxAttempts int
	Base        time.Duration
	Max         time.Duration
	Jitter      func(time.Duration) time.Duration
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts: 4,
		Base:        500 * time.Millisecond,
		Max:         30 * time.Second,
		Jitter:      fullJitter,
	}
}

// fullJitter tire uniformément dans [0, d] : évite que plusieurs workers
// rejouent en phase après un 429 commun.
func fullJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(d)) + 1)
}

// Retrying décore un Transport avec le rate limiter et la politique de retry.
// Le limiter est consulté avant chaque tentative, y compris les rejouées.
type Retrying struct {
	inner   Transport
	limiter Limiter
	policy  RetryPolicy
	clock   Clock
}

func NewRetrying(inner Transport, limiter Limiter, policy RetryPolicy, clock Clock) *Retrying {
	if clock == nil {
		clock = RealClock{}
	}
	if policy.Jitter == nil {
		policy.Jitter = fullJitter
	}
	return &Retrying{inner: inner, limiter: limiter, policy: policy, clock: clock}
}

func (r *Retrying) Execute(ctx context.Context, req APIRequest) (APIResponse, error) {
	var lastResp APIResponse
	var lastErr error

	for attempt := 0; attempt < r.policy.MaxAttempts; attempt++ {
		if r.limiter != nil {
			if err := r.limiter.Wait(ctx); err != nil {
				return APIResponse{}, err
			}
		}

		resp, err := r.inner.Execute(ctx, req)
		if err == nil {
			return resp, nil
		}
		lastResp, lastErr = resp, err

		// Une issue inconnue ne se rejoue jamais : la mutation a peut-être
		// abouti, la rejouer risque de la dupliquer.
		var unknown *OutcomeUnknownError
		if errors.As(err, &unknown) {
			return resp, err
		}
		// Un appel mal construit est un bug interne, pas un incident transitoire.
		var usageErr *UsageError
		if errors.As(err, &usageErr) {
			return resp, err
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) || !apiErr.Retryable() {
			return resp, err
		}
		if attempt == r.policy.MaxAttempts-1 {
			break
		}

		if err := r.clock.Sleep(ctx, r.backoff(attempt, resp)); err != nil {
			return resp, err
		}
	}
	return lastResp, lastErr
}

// backoff privilégie toujours Retry-After sur le calcul interne : le serveur
// sait mieux que nous quand il acceptera le prochain appel.
func (r *Retrying) backoff(attempt int, resp APIResponse) time.Duration {
	if d, ok := resp.RetryAfter(); ok {
		return d
	}
	d := time.Duration(float64(r.policy.Base) * math.Pow(2, float64(attempt)))
	if d > r.policy.Max {
		d = r.policy.Max
	}
	return r.policy.Jitter(d)
}
