// SPDX-License-Identifier: GPL-3.0-or-later

package preflight

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCheckNotInstalled(t *testing.T) {
	withEmptyPath(t)

	_, err := Check(context.Background(), "ntn")
	if !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("error = %v, want ErrNotInstalled", err)
	}
	// The message must say what to do.
	if !strings.Contains(err.Error(), "npm i -g ntn") {
		t.Errorf("message = %q, it must give the install command", err.Error())
	}
}

func TestCheckVersionTooOld(t *testing.T) {
	withFakeNtn(t, "version_old")

	_, err := Check(context.Background(), "ntn")
	var tooOld *TooOldError
	if !errors.As(err, &tooOld) {
		t.Fatalf("error = %v, want *TooOldError", err)
	}
	if tooOld.Found != "0.19.0" || tooOld.Minimum != MinNtnVersion {
		t.Errorf("got found=%q min=%q, want 0.19.0 / %s", tooOld.Found, tooOld.Minimum, MinNtnVersion)
	}
	msg := err.Error()
	for _, want := range []string{"0.19.0", MinNtnVersion, "ntn update"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message = %q, it must contain %q", msg, want)
		}
	}
}

func TestCheckSucceedsAndReportsWorkspace(t *testing.T) {
	withFakeNtn(t, "authenticated")

	info, err := Check(context.Background(), "ntn")
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if info.NtnVersion != "0.22.11" {
		t.Errorf("NtnVersion = %q, want %q", info.NtnVersion, "0.22.11")
	}
	if info.WorkspaceName != "Example Space" {
		t.Errorf("WorkspaceName = %q, want %q", info.WorkspaceName, "Example Space")
	}
	if info.WorkspaceID != "33333333-3333-4333-8333-333333333333" {
		t.Errorf("WorkspaceID = %q", info.WorkspaceID)
	}
	if info.BotEmail != "bot@example.com" {
		t.Errorf("BotEmail = %q", info.BotEmail)
	}
}

func TestCheckNotAuthenticated(t *testing.T) {
	withFakeNtn(t, "version_no_auth")

	_, err := Check(context.Background(), "ntn")
	if !errors.Is(err, ErrNotAuthenticated) {
		t.Fatalf("error = %v, want ErrNotAuthenticated", err)
	}
	if !strings.Contains(err.Error(), "notion-seed init") {
		t.Errorf("message = %q, it must point to `notion-seed init`", err.Error())
	}
	// The real cause must survive: without it, a network outage and a "not
	// logged in" produce the same message, and the user follows advice that
	// does not apply.
	if !strings.Contains(err.Error(), "cause:") {
		t.Errorf("message = %q, it must keep the cause reported by ntn", err.Error())
	}
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"ntn 0.22.11\n", "0.22.11"},
		{"ntn 1.0.0", "1.0.0"},
		{"  ntn 0.23.0  \n", "0.23.0"},
	}
	for _, tt := range tests {
		got, err := parseVersion([]byte(tt.in))
		if err != nil {
			t.Fatalf("parseVersion(%q) error = %v", tt.in, err)
		}
		if got != tt.want {
			t.Errorf("parseVersion(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestVersionAtLeast(t *testing.T) {
	tests := []struct {
		found, min string
		want       bool
	}{
		{"0.22.11", "0.22.11", true},
		{"0.22.12", "0.22.11", true},
		{"0.23.0", "0.22.11", true},
		{"1.0.0", "0.22.11", true},
		{"0.22.10", "0.22.11", false},
		{"0.19.0", "0.22.11", false},
		{"0.9.0", "0.22.11", false},
		// Canonical form of the trap: in the SAME field, a one-digit number
		// against a two-digit one. Lexicographically "0.22.2" > "0.22.11", so a
		// string comparison would return true here.
		{"0.22.2", "0.22.11", false},
	}
	for _, tt := range tests {
		if got := versionAtLeast(tt.found, tt.min); got != tt.want {
			t.Errorf("versionAtLeast(%q, %q) = %v, want %v", tt.found, tt.min, got, tt.want)
		}
	}
}

// Unexpected paths are as visible to the user as nominal paths: an
// unreadable ntn output must say what to do, not only what went wrong.
func TestUnreadableNtnOutputNamesTheCorrectiveAction(t *testing.T) {
	_, err := parseVersion([]byte("hello\n"))
	if err == nil {
		t.Fatal("parseVersion() error = nil, want an error")
	}
	for _, want := range []string{"→", MinNtnVersion} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("parseVersion: message = %q, it must contain %q", err.Error(), want)
		}
	}

	_, err = parseWhoami([]byte("a single column\n"))
	if err == nil {
		t.Fatal("parseWhoami() error = nil, want an error")
	}
	for _, want := range []string{"→", MinNtnVersion} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("parseWhoami: message = %q, it must contain %q", err.Error(), want)
		}
	}
}
