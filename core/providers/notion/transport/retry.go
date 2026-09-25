// SPDX-License-Identifier: GPL-3.0-or-later

package transport

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"time"
)

// RetryPolicy bounds the attempts. Only 429 and 5xx are replayed.
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

// fullJitter draws uniformly in [0, d]: it keeps several workers from
// replaying in phase after a shared 429.
func fullJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(d)) + 1)
}

// MaxRetryAfterWait caps the wait a server can impose through Retry-After.
// The header keeps priority over the computed backoff — the server knows
// better than notion-seed when it will accept the next call — but "takes
// priority over the computed value" does not mean "exempt from any absolute
// cap".
//
// Without a cap, a `retry-after: 60` — perfectly plausible from a real limiter
// — would block a READ-ONLY `plan` for three minutes in CI, and a hostile
// header would block forever. The value chosen is the computed backoff's cap
// (RetryPolicy.Max by default, 30 s): two different caps for the same question
// — "how long is it acceptable to wait between two attempts" — would be hard
// to justify.
//
// The context's remaining deadline was not chosen as the cap: in MVP 0, the
// plan context carries none (the timeout is set per call in NtnShell), so it
// would bound nothing.
const MaxRetryAfterWait = 30 * time.Second

// Retrying decorates a Transport with the rate limiter and the retry policy.
// The limiter is consulted before every attempt, including replayed ones.
type Retrying struct {
	inner   Transport
	limiter Limiter
	policy  RetryPolicy
	clock   Clock
	notify  func(string)
}

// NewRetrying panics if policy.MaxAttempts is less than 1, same contract as
// NewTokenBucket. Without this guard, a zero-value RetryPolicy makes Execute's
// loop never run and return (APIResponse{}, nil): a silent false success,
// without any call having been made. An apply would build its state on it.
//
// notify receives one line per wait. It may be nil, but a CLI must not leave
// it nil: a silent wait is indistinguishable from a hang.
func NewRetrying(inner Transport, limiter Limiter, policy RetryPolicy, clock Clock, notify func(string)) *Retrying {
	if policy.MaxAttempts < 1 {
		panic(fmt.Sprintf("NewRetrying: policy.MaxAttempts must be >= 1, got %d", policy.MaxAttempts))
	}
	if clock == nil {
		clock = RealClock{}
	}
	if policy.Jitter == nil {
		policy.Jitter = fullJitter
	}
	return &Retrying{inner: inner, limiter: limiter, policy: policy, clock: clock, notify: notify}
}

// isSafeMethod says whether replaying the request cannot create or modify
// anything twice. An empty method means GET: it is `ntn api`'s default when
// -X is missing, and notion-seed never leaves it empty on a mutation.
func isSafeMethod(method string) bool {
	return method == "" || method == "GET" || method == "HEAD"
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

		// An unknown outcome is never replayed: the mutation may have
		// succeeded, replaying it risks duplicating it.
		var unknown *OutcomeUnknownError
		if errors.As(err, &unknown) {
			return resp, err
		}
		// A badly built call is an internal bug, not a transient incident.
		var usageErr *UsageError
		if errors.As(err, &usageErr) {
			return resp, err
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) || !apiErr.Retryable() {
			return resp, err
		}
		// A 5xx on a MUTATING request carries the same ambiguity as a timeout:
		// a gateway can return 502 while Notion has already applied the
		// mutation. Replaying it would create a duplicate — exactly what apply's
		// incremental save exists to prevent. It is reclassified as an unknown
		// outcome, which stops dead and returns a message that sends the user
		// to check.
		//
		// A 429 is still replayed even on a mutation: the API explicitly says
		// it processed nothing, there is no ambiguity to clear.
		if apiErr.Status >= 500 && !isSafeMethod(req.Method) {
			return resp, &OutcomeUnknownError{Cause: err}
		}
		if attempt == r.policy.MaxAttempts-1 {
			break
		}

		wait, source := r.backoff(attempt, resp)
		r.announce(wait, source)
		if err := r.clock.Sleep(ctx, wait); err != nil {
			return resp, err
		}
	}
	return lastResp, lastErr
}

// announce tells the user that notion-seed is waiting, and why. A wait of
// several tens of seconds without a line of output is indistinguishable from
// a hang: measured, a `retry-after: 3` produced 9.35 s of total silence.
func (r *Retrying) announce(d time.Duration, source string) {
	if r.notify == nil || d <= 0 {
		return
	}
	r.notify(fmt.Sprintf("waiting %s before retrying (%s)",
		d.Round(time.Millisecond), source))
}

// backoff always prefers Retry-After over the internal computation: the
// server knows better than notion-seed when it will accept the next call. It
// stays capped by MaxRetryAfterWait. The second returned value names the
// source of the wait, so the printed message says it.
func (r *Retrying) backoff(attempt int, resp APIResponse) (time.Duration, string) {
	if d, ok := resp.RetryAfter(); ok {
		if d > MaxRetryAfterWait {
			return MaxRetryAfterWait, fmt.Sprintf(
				"Retry-After %s, capped at %s", d, MaxRetryAfterWait)
		}
		return d, "Retry-After"
	}
	d := time.Duration(float64(r.policy.Base) * math.Pow(2, float64(attempt)))
	// A pathological policy (huge Base, or many attempts) can push the
	// float64 -> int64 conversion out of range. The result then depends on the
	// architecture: amd64 returns a negative, arm64 saturates towards the
	// positive. A negative would bypass the clamp below and degenerate into a
	// zero sleep, hence an immediate retry.
	if d < 0 || d > r.policy.Max {
		d = r.policy.Max
	}
	return r.policy.Jitter(d), "backoff"
}
