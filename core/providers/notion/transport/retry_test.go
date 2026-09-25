// SPDX-License-Identifier: GPL-3.0-or-later

package transport

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// scriptedTransport replays a sequence of predefined responses and counts the
// calls received.
type scriptedTransport struct {
	calls  int
	script []struct {
		resp APIResponse
		err  error
	}
}

func (s *scriptedTransport) Execute(_ context.Context, _ APIRequest) (APIResponse, error) {
	i := s.calls
	s.calls++
	if i >= len(s.script) {
		return APIResponse{Status: 200}, nil
	}
	return s.script[i].resp, s.script[i].err
}

func scripted(entries ...struct {
	resp APIResponse
	err  error
}) *scriptedTransport {
	return &scriptedTransport{script: entries}
}

type entry = struct {
	resp APIResponse
	err  error
}

func noJitter(d time.Duration) time.Duration { return d }

func newTestRetrying(inner Transport, clock Clock) *Retrying {
	return newTestRetryingWithNotify(inner, clock, nil)
}

func newTestRetryingWithNotify(inner Transport, clock Clock, notify func(string)) *Retrying {
	p := DefaultRetryPolicy()
	p.Jitter = noJitter
	return NewRetrying(inner, NewTokenBucket(1000, 1000, clock), p, clock, notify)
}

func TestRetryingRetriesOn5xxThenSucceeds(t *testing.T) {
	clock := newFakeClock()
	inner := scripted(
		entry{APIResponse{Status: 502}, &APIError{Status: 502, NotionCode: "internal_server_error"}},
		entry{APIResponse{Status: 200, Body: []byte(`{"ok":true}`)}, nil},
	)
	r := newTestRetrying(inner, clock)

	resp, err := r.Execute(context.Background(), APIRequest{Path: "/v1/x"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if inner.calls != 2 {
		t.Errorf("calls = %d, want 2", inner.calls)
	}
	if resp.Status != 200 {
		t.Errorf("Status = %d, want 200", resp.Status)
	}
}

func TestRetryingDoesNotRetryOn4xx(t *testing.T) {
	clock := newFakeClock()
	inner := scripted(
		entry{APIResponse{Status: 404}, &APIError{Status: 404, NotionCode: "object_not_found"}},
	)
	r := newTestRetrying(inner, clock)

	_, err := r.Execute(context.Background(), APIRequest{Path: "/v1/x"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 404 {
		t.Fatalf("error = %v, want *APIError 404", err)
	}
	if inner.calls != 1 {
		t.Errorf("calls = %d, want 1 — a 4xx must never be replayed", inner.calls)
	}
}

// Retry-After keeps priority over the computed backoff, and the bound is
// EXACT: a lower bound alone would let through code that sleeps longer than
// requested.
func TestRetryingHonoursRetryAfterOverBackoff(t *testing.T) {
	clock := newFakeClock()
	inner := scripted(
		entry{
			APIResponse{Status: 429, Headers: map[string][]string{"retry-after": {"7"}}},
			&APIError{Status: 429, NotionCode: "rate_limited"},
		},
		entry{APIResponse{Status: 200}, nil},
	)
	r := newTestRetrying(inner, clock)

	if _, err := r.Execute(context.Background(), APIRequest{Path: "/v1/x"}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	// The base backoff is 500 ms; Retry-After: 7 must win, unchanged since 7s
	// stays under the cap.
	if got := clock.totalSlept(); got != 7*time.Second {
		t.Errorf("sleep = %v, want exactly 7s (Retry-After takes priority over the backoff)", got)
	}
}

// This test deliberately pinned the ABSENCE of a cap on Retry-After. It now
// pins both halves of the rule: the header keeps priority over the computed
// backoff, but stays bounded by an absolute cap. Without this cap,
// `retry-after: 60` — perfectly plausible from a real limiter — would block a
// read-only `plan` for three minutes in CI, and a hostile value would block
// forever.
func TestRetryingCapsRetryAfterButKeepsItsPriority(t *testing.T) {
	clock := newFakeClock()
	inner := scripted(
		entry{
			APIResponse{Status: 429, Headers: map[string][]string{"retry-after": {"120"}}},
			&APIError{Status: 429, NotionCode: "rate_limited"},
		},
		entry{APIResponse{Status: 200}, nil},
	)
	r := newTestRetrying(inner, clock)

	if _, err := r.Execute(context.Background(), APIRequest{Path: "/v1/x"}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := clock.totalSlept(); got != MaxRetryAfterWait {
		t.Errorf("sleep = %v, want %v — Retry-After must be capped", got, MaxRetryAfterWait)
	}
	// The priority, however, remains: the computed backoff of the first attempt
	// is 500 ms, and it is indeed the Retry-After cap that was applied.
	if MaxRetryAfterWait <= DefaultRetryPolicy().Base {
		t.Fatal("the test setup is wrong: the cap must be above the base backoff")
	}
}

// Values no sane server emits must not become waits. "-5" parsed into -5s
// (hence an immediate burst of replays) and "5m" into 5 milliseconds, both
// with ok=true.
func TestRetryAfterRejectsNonPositiveAndUnitSuffixes(t *testing.T) {
	tests := []struct {
		header string
		want   time.Duration
		wantOK bool
	}{
		{"3", 3 * time.Second, true},
		{" 3 ", 3 * time.Second, true},
		{"-5", 0, false},
		{"0", 0, false},
		{"5m", 0, false},
		{"1.5", 0, false},
		{"", 0, false},
		{"Wed, 21 Oct 2015 07:28:00 GMT", 0, false},
		{"99999999999999999999", 0, false},
	}
	for _, tt := range tests {
		resp := APIResponse{Headers: map[string][]string{"retry-after": {tt.header}}}
		got, ok := resp.RetryAfter()
		if ok != tt.wantOK || got != tt.want {
			t.Errorf("RetryAfter(%q) = %v, %v; want %v, %v", tt.header, got, ok, tt.want, tt.wantOK)
		}
	}
}

// A silent wait is indistinguishable from a hang: measured on the real
// binary, a `retry-after: 3` produced 9.35 s of total silence.
func TestRetryingAnnouncesEveryWait(t *testing.T) {
	clock := newFakeClock()
	inner := scripted(
		entry{
			APIResponse{Status: 429, Headers: map[string][]string{"retry-after": {"7"}}},
			&APIError{Status: 429, NotionCode: "rate_limited"},
		},
		entry{APIResponse{Status: 503}, &APIError{Status: 503}},
		entry{APIResponse{Status: 200}, nil},
	)
	var lines []string
	r := newTestRetryingWithNotify(inner, clock, func(s string) { lines = append(lines, s) })

	if _, err := r.Execute(context.Background(), APIRequest{Path: "/v1/x"}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("lines = %v, want one line per wait (2)", lines)
	}
	if !strings.Contains(lines[0], "7s") || !strings.Contains(lines[0], "Retry-After") {
		t.Errorf("first line = %q, it must say how long and why", lines[0])
	}
	if !strings.Contains(lines[1], "backoff") {
		t.Errorf("second line = %q, it must name the source of the wait", lines[1])
	}
}

func TestRetryingNeverRetriesOutcomeUnknown(t *testing.T) {
	clock := newFakeClock()
	inner := scripted(
		entry{APIResponse{}, &OutcomeUnknownError{Cause: context.DeadlineExceeded}},
	)
	r := newTestRetrying(inner, clock)

	_, err := r.Execute(context.Background(), APIRequest{Path: "/v1/x"})
	var unknown *OutcomeUnknownError
	if !errors.As(err, &unknown) {
		t.Fatalf("error = %v, want *OutcomeUnknownError", err)
	}
	if inner.calls != 1 {
		t.Errorf("calls = %d, want 1 — replaying an unknown outcome risks duplicating the mutation", inner.calls)
	}
}

func TestRetryingNeverRetriesUsageError(t *testing.T) {
	clock := newFakeClock()
	inner := scripted(entry{APIResponse{}, &UsageError{Stderr: "unexpected argument"}})
	r := newTestRetrying(inner, clock)

	_, err := r.Execute(context.Background(), APIRequest{Path: "/v1/x"})
	var usageErr *UsageError
	if !errors.As(err, &usageErr) {
		t.Fatalf("error = %v, want *UsageError", err)
	}
	if inner.calls != 1 {
		t.Errorf("calls = %d, want 1", inner.calls)
	}
}

// A 5xx on a MUTATING request is not replayed: a gateway can return 502 while
// Notion has already created the resource, and the replay would duplicate it.
// It is the same ambiguity as a timeout, hence the same outcome: unknown.
func TestRetryingDoesNotReplayServerErrorOnMutatingRequest(t *testing.T) {
	clock := newFakeClock()
	inner := scripted(
		entry{APIResponse{Status: 502}, &APIError{Status: 502, NotionCode: "internal_server_error"}},
		entry{APIResponse{Status: 200}, nil},
	)
	r := newTestRetrying(inner, clock)

	_, err := r.Execute(context.Background(), APIRequest{Method: "POST", Path: "/v1/databases"})
	if inner.calls != 1 {
		t.Errorf("calls = %d, want 1: a mutation is not replayed on 5xx", inner.calls)
	}
	var unknown *OutcomeUnknownError
	if !errors.As(err, &unknown) {
		t.Fatalf("error = %v (%T), want an OutcomeUnknownError", err, err)
	}
	// The original error must stay readable: it is what says what happened on
	// the API side.
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 502 {
		t.Errorf("error = %v, it must keep wrapping the 502", err)
	}
}

// A 429 is replayed even on a mutation: the API explicitly says it processed
// nothing, there is no ambiguity to clear.
func TestRetryingStillReplaysRateLimitOnMutatingRequest(t *testing.T) {
	clock := newFakeClock()
	inner := scripted(
		entry{APIResponse{Status: 429}, &APIError{Status: 429, NotionCode: "rate_limited"}},
		entry{APIResponse{Status: 200, Body: []byte(`{"ok":true}`)}, nil},
	)
	r := newTestRetrying(inner, clock)

	if _, err := r.Execute(context.Background(), APIRequest{Method: "POST", Path: "/v1/databases"}); err != nil {
		t.Fatalf("Execute() error = %v, want nil after replay", err)
	}
	if inner.calls != 2 {
		t.Errorf("calls = %d, want 2", inner.calls)
	}
}

// A read is still replayed on 5xx: replaying it cannot duplicate anything.
func TestRetryingStillReplaysServerErrorOnRead(t *testing.T) {
	clock := newFakeClock()
	inner := scripted(
		entry{APIResponse{Status: 502}, &APIError{Status: 502}},
		entry{APIResponse{Status: 200, Body: []byte(`{"ok":true}`)}, nil},
	)
	r := newTestRetrying(inner, clock)

	if _, err := r.Execute(context.Background(), APIRequest{Method: "GET", Path: "/v1/databases/db1"}); err != nil {
		t.Fatalf("Execute() error = %v, want nil after replay", err)
	}
	if inner.calls != 2 {
		t.Errorf("calls = %d, want 2", inner.calls)
	}
}

func TestRetryingGivesUpAfterMaxAttempts(t *testing.T) {
	clock := newFakeClock()
	inner := scripted(
		entry{APIResponse{Status: 503}, &APIError{Status: 503}},
		entry{APIResponse{Status: 503}, &APIError{Status: 503}},
		entry{APIResponse{Status: 503}, &APIError{Status: 503}},
		entry{APIResponse{Status: 503}, &APIError{Status: 503}},
		entry{APIResponse{Status: 503}, &APIError{Status: 503}},
	)
	r := newTestRetrying(inner, clock)

	if _, err := r.Execute(context.Background(), APIRequest{Path: "/v1/x"}); err == nil {
		t.Fatal("Execute() error = nil, want an error after the attempts are exhausted")
	}
	if inner.calls != DefaultRetryPolicy().MaxAttempts {
		t.Errorf("calls = %d, want %d", inner.calls, DefaultRetryPolicy().MaxAttempts)
	}
}
