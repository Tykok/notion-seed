// SPDX-License-Identifier: GPL-3.0-or-later

package transport

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// APIRequest describes a call to the Notion public API.
type APIRequest struct {
	Method  string
	Path    string
	Body    []byte        // JSON. Passed to ntn via -d @- on stdin.
	Timeout time.Duration // Required: ntn api has no internal timeout.
}

// APIResponse holds the result of a call. Status and Headers come from ntn's
// verbose stderr; Body comes from its stdout.
type APIResponse struct {
	Status  int
	Headers map[string][]string
	Body    []byte
}

// Transport executes a call to the Notion API. The only implementation in
// MVP 0 is NtnShell, a shell-out to `ntn api`.
type Transport interface {
	Execute(ctx context.Context, req APIRequest) (APIResponse, error)
}

// MaxPlausibleRetryAfter bounds the value accepted from the header. Beyond
// it, the value is treated as unreadable rather than as an instruction:
// converting it to a duration could overflow int64, and no value of that
// order makes sense for a single call. The computed backoff then takes over.
const MaxPlausibleRetryAfter = 24 * time.Hour

// RetryAfter reads the Retry-After header and returns it as a duration. ok is
// false if the header is missing, unreadable, or holds an unusable value.
//
// Only the "integer seconds" form is accepted, the only one the Notion API
// emits. "s" is no longer appended to the raw value: "5m" became 5
// milliseconds instead of being rejected, and "-5" parsed into a negative
// duration, hence three immediate replays in a burst. The HTTP-date form of
// the header is not handled; it comes out as ok=false, which hands control
// back to the computed backoff — a safe degradation, never a wrong wait.
func (r APIResponse) RetryAfter() (d time.Duration, ok bool) {
	vals := r.Headers["retry-after"]
	if len(vals) == 0 {
		return 0, false
	}
	secs, err := strconv.Atoi(strings.TrimSpace(vals[0]))
	if err != nil || secs <= 0 {
		return 0, false
	}
	if secs > int(MaxPlausibleRetryAfter/time.Second) {
		return 0, false
	}
	return time.Duration(secs) * time.Second, true
}
