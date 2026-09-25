// SPDX-License-Identifier: GPL-3.0-or-later

package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/tykok/notion-seed/core/preflight"
)

// DefaultNotionVersion is passed explicitly on every call. ntn has its own
// default, which moves with its versions; it is never inherited.
const DefaultNotionVersion = "2025-09-03"

// DefaultTimeout bounds each call. ntn api has no internal timeout and was
// observed hanging for several minutes without producing a single byte.
const DefaultTimeout = 30 * time.Second

// NtnShell is the only Transport implementation in MVP 0: a shell-out to
// `ntn api`. ntn serves as both transport and authentication.
type NtnShell struct {
	Binary         string
	NotionVersion  string
	DefaultTimeout time.Duration
}

func NewNtnShell() *NtnShell {
	return &NtnShell{
		Binary:         "ntn",
		NotionVersion:  DefaultNotionVersion,
		DefaultTimeout: DefaultTimeout,
	}
}

func (t *NtnShell) Execute(ctx context.Context, req APIRequest) (APIResponse, error) {
	if err := checkPath(req.Path); err != nil {
		return APIResponse{}, &UsageError{
			Command: t.Binary + " api " + req.Path,
			Stderr:  err.Error(),
		}
	}

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = t.DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{"-v", "api", "--notion-version", t.NotionVersion}
	if req.Method != "" {
		args = append(args, "-X", req.Method)
	}
	args = append(args, req.Path)
	if len(req.Body) > 0 {
		args = append(args, "-d", "@-")
	}

	cmd := exec.CommandContext(ctx, t.Binary, args...)

	// With a body, it goes on stdin (-d @-) and its EOF comes from the end of
	// the read. Without a body, cmd.Stdin is left nil: os/exec then gives
	// /dev/null to the child, which is exactly what is wanted.
	//
	// NEVER assign os.Stdin here. `ntn api` treats stdin as a valid body
	// source, so a stdin without EOF makes it wait forever — measured: 2 ms
	// with a nil Stdin, hanging until killed with os.Stdin.
	if len(req.Body) > 0 {
		cmd.Stdin = bytes.NewReader(req.Body)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	// A timeout does not say whether the mutation was applied server-side.
	if ctx.Err() != nil {
		return APIResponse{}, &OutcomeUnknownError{Cause: ctx.Err()}
	}

	status, headers, hasStatus, parseErr := ParseStatusAndHeaders(stderr.Bytes())
	resp := APIResponse{Status: status, Headers: headers, Body: stdout.Bytes()}

	if runErr == nil {
		// Exit 0 is not enough: the status comes ONLY from the -v trace.
		// Without a status line, there is no knowing whether the API was
		// reached, nor with which code — returning (resp, nil) would report a
		// success nobody observed. It is the risk named by the spec, "ntn
		// changes its output format", and it must fail red.
		if parseErr != nil {
			return resp, fmt.Errorf("%w\n  → %s", parseErr, preflight.PinNtnHint())
		}
		if !hasStatus {
			return resp, fmt.Errorf(
				"ntn exited with 0 but its -v trace contains no status line: "+
					"notion-seed cannot verify that the call succeeded\n  → %s",
				preflight.PinNtnHint())
		}
		// Nothing downstream looks at resp.Status: if ntn returned a 4xx/5xx
		// while exiting with 0, the error would pass for a success. It is
		// reclassified here, in the only place that sees the status.
		if status >= 400 {
			if apiErr, ok := ParseAPIError(stderr.Bytes()); ok {
				return resp, apiErr
			}
			return resp, &APIError{
				Status:     status,
				NotionCode: "unparsed",
				Message:    strings.TrimSpace(stderr.String()),
			}
		}
		return resp, nil
	}

	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) {
		// ntn not found, not executable: no call was made, so the outcome is
		// known — it is a failure.
		return APIResponse{}, runErr
	}

	switch exitErr.ExitCode() {
	case 2:
		return resp, &UsageError{
			Command: t.Binary + " " + strings.Join(args, " "),
			Stderr:  strings.TrimSpace(stderr.String()),
		}
	case 5:
		if apiErr, ok := ParseAPIError(stderr.Bytes()); ok {
			return resp, apiErr
		}
	}

	if hasStatus {
		return resp, &APIError{
			Status:     status,
			NotionCode: "unparsed",
			Message:    strings.TrimSpace(stderr.String()),
		}
	}
	// A non-zero exit AND an unreadable -v trace (truncated, or a format the
	// scanner rejects): parseErr was lost here, so the failure degraded into
	// a generic OutcomeUnknownError without saying the trace itself was at
	// fault — exactly the diagnosis the other branch (exit 0) already gives.
	// The OutcomeUnknownError classification stays correct: an unreadable
	// trace on a non-zero exit still does not say what happened.
	if parseErr != nil {
		return resp, &OutcomeUnknownError{
			Cause: fmt.Errorf("%w\n  → %s", parseErr, preflight.PinNtnHint()),
		}
	}
	return resp, &OutcomeUnknownError{Cause: runErr}
}

// checkPath rejects a path notion-seed should not have built. No injection is
// possible today — argv goes out as separate elements, no shell is involved —
// but a page id equal to "../../v1/users" produces a path the HTTP layer can
// normalize to an endpoint other than the intended one. The guard is here, as
// close to the call as possible, on top of the UUID `pattern` the schema
// enforces on parent_page_id.
func checkPath(p string) error {
	if !strings.HasPrefix(p, "/") {
		return fmt.Errorf("API path %q: it must start with \"/\"", p)
	}
	if p != path.Clean(p) {
		return fmt.Errorf(
			"API path %q: it contains a relative segment, which can target an endpoint other than the intended one", p)
	}
	return nil
}
