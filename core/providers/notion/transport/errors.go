// SPDX-License-Identifier: GPL-3.0-or-later

package transport

import "fmt"

// APIError is an error returned by the Notion API, extracted from ntn's
// stderr (exit code 5).
type APIError struct {
	Status     int
	NotionCode string
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("notion api %d %s: %s", e.Status, e.NotionCode, e.Message)
}

// Retryable is true only on 429 and 5xx. A 4xx is a config or permission
// error: replaying it only wastes time.
func (e *APIError) Retryable() bool {
	return e.Status == 429 || e.Status >= 500
}

// OutcomeUnknownError reports that it is not known whether the call succeeded
// server-side — typically a timeout. Never treat it as a failure: the
// mutation may have been applied.
type OutcomeUnknownError struct {
	Cause error
}

func (e *OutcomeUnknownError) Error() string {
	return fmt.Sprintf("unknown result, the call may have succeeded server-side: %v", e.Cause)
}

func (e *OutcomeUnknownError) Unwrap() error { return e.Cause }

// UsageError reports that notion-seed built an invalid `ntn` call (exit
// code 2). It is an internal bug, never replayed. The built command is
// reported: without it, the user has no way to report the bug.
type UsageError struct {
	Command string
	Stderr  string
}

func (e *UsageError) Error() string {
	return fmt.Sprintf(
		"invalid ntn call (notion-seed bug)\n  command: %s\n  %s",
		e.Command, e.Stderr)
}
