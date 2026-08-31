package transport

import (
	"context"
	"errors"
	"strings"
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

// Retry-After garde la priorité sur le backoff calculé, et la borne est
// EXACTE : une borne inférieure seule laisserait passer un code qui dort plus
// que demandé.
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
	// Le backoff de base est de 500 ms ; Retry-After: 7 doit gagner, à
	// l'identique puisque 7s reste sous le plafond.
	if got := clock.totalSlept(); got != 7*time.Second {
		t.Errorf("sommeil = %v, want exactement 7s (Retry-After prioritaire sur le backoff)", got)
	}
}

// Ce test épinglait délibérément l'ABSENCE de plafond sur Retry-After. Il
// épingle désormais les deux moitiés de la règle : le header garde la priorité
// sur le backoff calculé, mais reste borné par un plafond absolu. Sans ce
// plafond, `retry-after: 60` — parfaitement plausible d'un vrai limiteur —
// bloquerait un `plan` en lecture seule trois minutes en CI, et une valeur
// hostile bloquerait indéfiniment.
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
		t.Errorf("sommeil = %v, want %v — Retry-After doit être plafonné", got, MaxRetryAfterWait)
	}
	// La priorité, elle, reste : le backoff calculé de la première tentative
	// vaut 500 ms, et c'est bien le plafond de Retry-After qui a été appliqué.
	if MaxRetryAfterWait <= DefaultRetryPolicy().Base {
		t.Fatal("le montage du test est faux : le plafond doit être au-dessus du backoff de base")
	}
}

// Les valeurs qu'aucun serveur sain n'émet ne doivent pas devenir des attentes.
// "-5" se parsait en -5s (donc en rejeu immédiat en rafale) et "5m" en 5
// millisecondes, tous deux avec ok=true.
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
			t.Errorf("RetryAfter(%q) = %v, %v ; want %v, %v", tt.header, got, ok, tt.want, tt.wantOK)
		}
	}
}

// Une attente muette est indistinguable d'un blocage : mesuré sur le vrai
// binaire, un `retry-after: 3` produisait 9,35 s de silence total.
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
		t.Fatalf("lignes = %v, want une ligne par attente (2)", lines)
	}
	if !strings.Contains(lines[0], "7s") || !strings.Contains(lines[0], "Retry-After") {
		t.Errorf("première ligne = %q, elle doit dire combien de temps et pourquoi", lines[0])
	}
	if !strings.Contains(lines[1], "backoff") {
		t.Errorf("deuxième ligne = %q, elle doit nommer la source de l'attente", lines[1])
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
