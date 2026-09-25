// SPDX-License-Identifier: GPL-3.0-or-later

// Package preflight checks that the environment can run notion-seed: ntn
// present, recent enough, and authenticated.
package preflight

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// MinNtnVersion is the only version against which ntn's output contract was
// measured. Raise it when a more recent one is validated, never assume
// backward compatibility: notion-seed parses ntn's stdout and stderr, so a
// format change breaks it silently.
const MinNtnVersion = "0.22.11"

var (
	ErrNotInstalled     = errors.New("ntn is not installed")
	ErrNotAuthenticated = errors.New("ntn is not authenticated")
)

// NotAuthenticatedError reports missing or invalid authentication, while
// KEEPING the real cause: a network failure, an internal ntn crash or a
// context cancellation are not "not logged in". The type exists so that `init`
// can give its own advice without losing the detail — printing "not
// authenticated" for a network outage, on the command whose only job is to
// diagnose the environment, sends the user looking in the wrong place.
type NotAuthenticatedError struct {
	Cause error
}

func (e *NotAuthenticatedError) Error() string {
	return fmt.Sprintf("%v — run `notion-seed init` to sign in (cause: %v)",
		ErrNotAuthenticated, e.Cause)
}

// Unwrap returns both: errors.Is(err, ErrNotAuthenticated) stays true for
// callers, and the cause stays inspectable.
func (e *NotAuthenticatedError) Unwrap() []error {
	return []error{ErrNotAuthenticated, e.Cause}
}

// TooOldError reports an ntn that is present but too old.
type TooOldError struct {
	Found   string
	Minimum string
}

func (e *TooOldError) Error() string {
	return fmt.Sprintf(
		"ntn %s is too old, notion-seed requires at least %s — update it with `ntn update`",
		e.Found, e.Minimum)
}

// PinNtnHint is the corrective action shared by every place where ntn's
// output contract can break: notion-seed reads an output format, and only one
// version of it was measured. A single source of truth, so the message and
// the required version never diverge.
func PinNtnHint() string {
	return fmt.Sprintf(
		"pin ntn %s (`npm i -g ntn@%s`), the only version whose output format was measured",
		MinNtnVersion, MinNtnVersion)
}

// Info describes the validated environment.
type Info struct {
	NtnVersion    string
	WorkspaceID   string
	WorkspaceName string
	BotEmail      string
}

// Check verifies ntn's presence, version and authentication.
func Check(ctx context.Context, binary string) (Info, error) {
	if _, err := exec.LookPath(binary); err != nil {
		return Info{}, fmt.Errorf(
			"%w — install it with `npm i -g ntn`, then run `notion-seed init`",
			ErrNotInstalled)
	}

	versionOut, err := run(ctx, binary, "--version")
	if err != nil {
		return Info{}, fmt.Errorf(
			"`%s --version` failed: %w\n"+
				"  → %s is present but does not respond; reinstall it with "+
				"`npm i -g ntn@%s`, then rerun `notion-seed init`",
			binary, err, binary, MinNtnVersion)
	}
	version, err := parseVersion(versionOut)
	if err != nil {
		return Info{}, err
	}
	if !versionAtLeast(version, MinNtnVersion) {
		return Info{}, &TooOldError{Found: version, Minimum: MinNtnVersion}
	}

	whoamiOut, err := run(ctx, binary, "whoami")
	if err != nil {
		return Info{}, &NotAuthenticatedError{Cause: err}
	}
	info, err := parseWhoami(whoamiOut)
	if err != nil {
		return Info{}, err
	}
	info.NtnVersion = version
	return info, nil
}

func run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// cmd.Stdin left nil: os/exec gives /dev/null to the child. Never set it to
	// os.Stdin, which `ntn` would take for a body without EOF.
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// parseVersion reads the output of `ntn --version`, of the form "ntn 0.22.11".
func parseVersion(out []byte) (string, error) {
	fields := strings.Fields(string(out))
	if len(fields) < 2 {
		return "", fmt.Errorf("unreadable `ntn --version` output: %q\n  → %s",
			strings.TrimSpace(string(out)), PinNtnHint())
	}
	return fields[len(fields)-1], nil
}

// parseWhoami reads the output of `ntn whoami`, a line of tab-separated
// fields: bot_id, bot_name, "bot", email, workspace_id, workspace_name,
// user_id, user_name, "person".
func parseWhoami(out []byte) (Info, error) {
	line := strings.TrimSpace(string(out))
	fields := strings.Split(line, "\t")
	if len(fields) < 6 {
		return Info{}, fmt.Errorf(
			"unreadable `ntn whoami` output (%d fields, at least 6 expected): %q\n"+
				"  → %s; or run `ntn login` again if the session expired",
			len(fields), line, PinNtnHint())
	}
	return Info{
		BotEmail:      fields[3],
		WorkspaceID:   fields[4],
		WorkspaceName: fields[5],
	}, nil
}

// versionAtLeast compares two semver-ish versions field by field. A
// non-numeric field (pre-release) is treated as 0.
func versionAtLeast(found, minimum string) bool {
	f := splitVersion(found)
	m := splitVersion(minimum)
	for i := 0; i < 3; i++ {
		switch {
		case f[i] > m[i]:
			return true
		case f[i] < m[i]:
			return false
		}
	}
	return true
}

func splitVersion(v string) [3]int {
	var out [3]int
	parts := strings.SplitN(strings.TrimPrefix(v, "v"), ".", 4)
	for i := 0; i < 3 && i < len(parts); i++ {
		numeric := parts[i]
		if idx := strings.IndexFunc(numeric, func(r rune) bool {
			return r < '0' || r > '9'
		}); idx >= 0 {
			numeric = numeric[:idx]
		}
		out[i], _ = strconv.Atoi(numeric)
	}
	return out
}
