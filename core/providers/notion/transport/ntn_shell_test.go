// SPDX-License-Identifier: GPL-3.0-or-later

package transport

import (
	"context"
	"errors"
	"slices"
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
		t.Error("a 404 must not be retryable")
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
	// The built command must appear in the message: it is an internal bug,
	// the user must be able to report it as is.
	if !strings.Contains(usageErr.Command, "--notion-version") {
		t.Errorf("Command = %q, it must contain the built command", usageErr.Command)
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
		t.Errorf("the timeout did not interrupt the process: %v", elapsed)
	}
}

// This test ties the parsing to the type: a real 429 from ntn must produce a
// retryable APIError AND a readable Retry-After. That pair is what the retry
// policy consumes; testing it on strings alone would not prove the whole
// chain works.
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
		t.Fatal("RetryAfter() ok = false, want true — the header was not parsed")
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

// The argv is compared as a LIST, with exact equality. A substring assertion
// could not fail: `strings.Contains(argv, "-v")` is satisfied by
// `--notion-version` alone, so removing -v from the argv left the test green
// — while the -v trace is the only source of the HTTP status and the
// Retry-After header.
func TestExecuteAlwaysPassesVerboseAndNotionVersion(t *testing.T) {
	withFakeNtn(t, "echo_argv")
	tr := NewNtnShell()

	resp, err := tr.Execute(context.Background(), APIRequest{Method: "PATCH", Path: "/v1/x"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := []string{"-v", "api", "--notion-version", DefaultNotionVersion, "-X", "PATCH", "/v1/x"}
	if got := echoedArgv(resp.Body); !slices.Equal(got, want) {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

// echoedArgv reads back the output of the echo_argv scenario: one argument per line.
func echoedArgv(body []byte) []string {
	trimmed := strings.TrimRight(string(body), "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
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
		t.Errorf("body received by ntn = %q, want %q", resp.Body, body)
	}
}

// Without a body, no -d @- must be passed: passing it without feeding stdin
// would make ntn wait for a body that never comes. Exact equality here too:
// looking for the absence of the "-d" substring says nothing about what is
// present.
func TestExecuteWithoutBodyPassesNoDataFlag(t *testing.T) {
	withFakeNtn(t, "echo_argv")
	tr := NewNtnShell()

	resp, err := tr.Execute(context.Background(), APIRequest{Method: "GET", Path: "/v1/x"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := []string{"-v", "api", "--notion-version", DefaultNotionVersion, "-X", "GET", "/v1/x"}
	if got := echoedArgv(resp.Body); !slices.Equal(got, want) {
		t.Errorf("argv = %q, want %q — no -d @- without a body", got, want)
	}
}

// An exit 0 without a status line is the risk named by the spec: ntn changes
// its output format. It must fail red, never return a success nobody
// observed — otherwise a plan declares the parent page readable without
// having read a single status, and an apply would report an unverified
// mutation.
func TestExecuteExitZeroWithoutStatusLineIsAnError(t *testing.T) {
	withFakeNtn(t, "exit0_no_status")
	tr := NewNtnShell()

	resp, err := tr.Execute(context.Background(), APIRequest{Method: "GET", Path: "/v1/x"})
	if err == nil {
		t.Fatalf("Execute() error = nil while the -v trace carries no status (resp = %+v)", resp)
	}
	for _, want := range []string{"status line", "0.22.11"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, it must contain %q", err.Error(), want)
		}
	}
}

// Re-review leftover: on a non-zero exit, parseErr was lost — a truncated or
// unreadable -v trace degraded into a generic OutcomeUnknownError, without
// saying the trace itself was at fault. Mirror of
// TestExecuteExitZeroWithoutStatusLineIsAnError, on the other branch.
func TestExecuteNonZeroExitWithUnreadableTraceKeepsParseHint(t *testing.T) {
	withFakeNtn(t, "exit1_unreadable_trace")
	tr := NewNtnShell()

	_, err := tr.Execute(context.Background(), APIRequest{Method: "GET", Path: "/v1/x"})
	if err == nil {
		t.Fatal("Execute() error = nil, want an error on an unreadable -v trace")
	}
	// The classification stays OutcomeUnknownError: a non-zero exit with an
	// unreadable trace still does not say what happened server-side.
	var unknown *OutcomeUnknownError
	if !errors.As(err, &unknown) {
		t.Fatalf("error = %v (%T), want *OutcomeUnknownError", err, err)
	}
	if !strings.Contains(err.Error(), "0.22.11") {
		t.Errorf("message = %q, it must keep the advice to pin ntn 0.22.11", err.Error())
	}
}

// A 4xx returned with an exit 0 must not pass for a success: the status is
// checked nowhere downstream, so this is where it must be reclassified.
func TestExecuteStatusAtLeast400IsAnErrorEvenOnExitZero(t *testing.T) {
	withFakeNtn(t, "exit0_status_403")
	tr := NewNtnShell()

	_, err := tr.Execute(context.Background(), APIRequest{Method: "GET", Path: "/v1/x"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v (%T), want *APIError", err, err)
	}
	if apiErr.Status != 403 {
		t.Errorf("Status = %d, want 403", apiErr.Status)
	}
	if apiErr.Retryable() {
		t.Error("a 403 must not be retryable")
	}
}

// A relative API path is rejected before any call: normalized by the HTTP
// layer, it would target an endpoint other than the intended one.
func TestExecuteRejectsRelativePath(t *testing.T) {
	withFakeNtn(t, "ok")
	tr := NewNtnShell()

	for _, bad := range []string{"v1/pages/abc", "/v1/pages/../../v1/users"} {
		_, err := tr.Execute(context.Background(), APIRequest{Method: "GET", Path: bad})
		var usageErr *UsageError
		if !errors.As(err, &usageErr) {
			t.Errorf("Execute(%q) error = %v (%T), want *UsageError", bad, err, err)
		}
	}
}

// A binary that is not found made NO call: the outcome is known, it is a
// plain failure. Classifying it as OutcomeUnknownError would be the mirror
// error of the timeout's.
func TestExecuteBinaryNotFoundIsAKnownFailure(t *testing.T) {
	tr := NewNtnShell()
	tr.Binary = "notion-seed-nonexistent-binary-xyz"

	start := time.Now()
	_, err := tr.Execute(context.Background(), APIRequest{Method: "GET", Path: "/v1/x"})
	if err == nil {
		t.Fatal("Execute() error = nil, want an error")
	}
	var unknown *OutcomeUnknownError
	if errors.As(err, &unknown) {
		t.Error("missing binary classified as OutcomeUnknownError: no call was made, the outcome is known")
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		t.Error("missing binary classified as APIError: the API was never contacted")
	}
	var usageErr *UsageError
	if errors.As(err, &usageErr) {
		t.Error("missing binary classified as UsageError: the call was valid, the binary is what is missing")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took %v: a missing binary must fail immediately", elapsed)
	}
}
