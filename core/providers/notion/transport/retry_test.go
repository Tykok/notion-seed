package transport

import (
	"context"
	"errors"
	"testing"
	"time"
)

// scriptedTransport rejoue une suite de réponses prédéfinies et compte les
// appels reçus.
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
	p := DefaultRetryPolicy()
	p.Jitter = noJitter
	return NewRetrying(inner, NewTokenBucket(1000, 1000, clock), p, clock)
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
		t.Errorf("appels = %d, want 2", inner.calls)
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
		t.Errorf("appels = %d, want 1 — un 4xx ne doit jamais être rejoué", inner.calls)
	}
}

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
	// Le backoff de base est de 500 ms ; Retry-After: 7 doit gagner.
	if got := clock.totalSlept(); got < 7*time.Second {
		t.Errorf("sommeil = %v, want >= 7s (Retry-After prioritaire sur le backoff)", got)
	}
}

// Retry-After doit sortir TEL QUEL, sans être plafonné par policy.Max. Le test
// précédent utilise 7s < Max, donc il passerait même si le code clampait ; il
// faut une valeur au-dessus du plafond pour vérifier cette moitié de la règle.
func TestRetryingDoesNotClampRetryAfterByMax(t *testing.T) {
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
	// Max vaut 30s ; le serveur a demandé 120s. C'est le serveur qui gagne.
	if got := clock.totalSlept(); got < 120*time.Second {
		t.Errorf("sommeil = %v, want >= 120s — Retry-After a été plafonné par Max", got)
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
		t.Errorf("appels = %d, want 1 — rejouer une issue inconnue risque de dupliquer la mutation", inner.calls)
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
		t.Errorf("appels = %d, want 1", inner.calls)
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
		t.Fatal("Execute() error = nil, want une erreur après épuisement des tentatives")
	}
	if inner.calls != DefaultRetryPolicy().MaxAttempts {
		t.Errorf("appels = %d, want %d", inner.calls, DefaultRetryPolicy().MaxAttempts)
	}
}
