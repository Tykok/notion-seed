package transport

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestExecuteSuccessParsesStatusAndBody(t *testing.T) {
	withFakeNtn(t, "ok")
	tr := NewNtnShell()

	resp, err := tr.Execute(context.Background(), APIRequest{Method: "GET", Path: "/v1/x"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if resp.Status != 200 {
		t.Errorf("Status = %d, want 200", resp.Status)
	}
	if !strings.Contains(string(resp.Body), `"object":"data_source"`) {
		t.Errorf("Body = %q, want the JSON payload", resp.Body)
	}
}

func TestExecuteAPIErrorIsTyped(t *testing.T) {
	withFakeNtn(t, "not_found")
	tr := NewNtnShell()

	_, err := tr.Execute(context.Background(), APIRequest{Method: "GET", Path: "/v1/x"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v (%T), want *APIError", err, err)
	}
	if apiErr.Status != 404 || apiErr.NotionCode != "object_not_found" {
		t.Errorf("got %d %q, want 404 object_not_found", apiErr.Status, apiErr.NotionCode)
	}
	if apiErr.Retryable() {
		t.Error("un 404 ne doit pas être retryable")
	}
}

func TestExecuteUsageErrorIsTypedAndNotRetryable(t *testing.T) {
	withFakeNtn(t, "usage_error")
	tr := NewNtnShell()

	_, err := tr.Execute(context.Background(), APIRequest{Method: "GET", Path: "/v1/x"})
	var usageErr *UsageError
	if !errors.As(err, &usageErr) {
		t.Fatalf("error = %v (%T), want *UsageError", err, err)
	}
	// La commande construite doit figurer dans le message : c'est un bug
	// interne, l'utilisateur doit pouvoir le rapporter tel quel.
	if !strings.Contains(usageErr.Command, "--notion-version") {
		t.Errorf("Command = %q, elle doit contenir la commande construite", usageErr.Command)
	}
}

func TestExecuteTimeoutIsOutcomeUnknown(t *testing.T) {
	withFakeNtn(t, "hang")
	tr := NewNtnShell()

	start := time.Now()
	_, err := tr.Execute(context.Background(), APIRequest{
		Method:  "POST",
		Path:    "/v1/databases",
		Body:    []byte(`{"a":1}`),
		Timeout: 200 * time.Millisecond,
	})
	elapsed := time.Since(start)

	var unknown *OutcomeUnknownError
	if !errors.As(err, &unknown) {
		t.Fatalf("error = %v (%T), want *OutcomeUnknownError", err, err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("le timeout n'a pas interrompu le process: %v", elapsed)
	}
}

// Ce test relie le parsing au type : un 429 réel de ntn doit produire une
// APIError retryable ET un Retry-After lisible. C'est ce couple que la
// politique de retry consomme ; le tester sur des chaînes seules ne prouverait
// pas que la chaîne complète fonctionne.
func TestExecuteRateLimitedExposesRetryAfter(t *testing.T) {
	withFakeNtn(t, "rate_limited")
	tr := NewNtnShell()

	resp, err := tr.Execute(context.Background(), APIRequest{
		Method: "POST", Path: "/v1/pages", Body: []byte(`{}`),
	})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v (%T), want *APIError", err, err)
	}
	if apiErr.Status != 429 || !apiErr.Retryable() {
		t.Errorf("got %d retryable=%v, want 429 retryable", apiErr.Status, apiErr.Retryable())
	}
	d, ok := resp.RetryAfter()
	if !ok {
		t.Fatal("RetryAfter() ok = false, want true — le header n'a pas été parsé")
	}
	if d != time.Second {
		t.Errorf("RetryAfter() = %v, want 1s", d)
	}
}

func TestExecuteServerErrorIsRetryable(t *testing.T) {
	withFakeNtn(t, "server_error")
	tr := NewNtnShell()

	_, err := tr.Execute(context.Background(), APIRequest{Method: "GET", Path: "/v1/x"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v (%T), want *APIError", err, err)
	}
	if apiErr.Status != 502 || !apiErr.Retryable() {
		t.Errorf("got %d retryable=%v, want 502 retryable", apiErr.Status, apiErr.Retryable())
	}
}

func TestExecuteAlwaysPassesVerboseAndNotionVersion(t *testing.T) {
	withFakeNtn(t, "echo_argv")
	tr := NewNtnShell()

	resp, err := tr.Execute(context.Background(), APIRequest{Method: "PATCH", Path: "/v1/x"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	argv := string(resp.Body)
	for _, want := range []string{"-v", "api", "--notion-version", DefaultNotionVersion, "-X", "PATCH", "/v1/x"} {
		if !strings.Contains(argv, want) {
			t.Errorf("argv %q ne contient pas %q", argv, want)
		}
	}
}

func TestExecuteSendsBodyOnStdinWithDashData(t *testing.T) {
	withFakeNtn(t, "echo_stdin")
	tr := NewNtnShell()

	body := []byte(`{"parent":{"type":"page_id","page_id":"abc"}}`)
	resp, err := tr.Execute(context.Background(), APIRequest{
		Method: "POST", Path: "/v1/databases", Body: body,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if string(resp.Body) != string(body) {
		t.Errorf("body reçu par ntn = %q, want %q", resp.Body, body)
	}
}

func TestExecuteWithoutBodyDoesNotHangOnStdin(t *testing.T) {
	withFakeNtn(t, "ok")
	tr := NewNtnShell()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = tr.Execute(context.Background(), APIRequest{Method: "GET", Path: "/v1/x"})
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Execute a bloqué sans body : stdin n'est pas explicitement défini")
	}
}
