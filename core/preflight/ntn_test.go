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
	// Le message doit dire quoi faire.
	if !strings.Contains(err.Error(), "npm i -g ntn") {
		t.Errorf("message = %q, il doit donner la commande d'installation", err.Error())
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
			t.Errorf("message = %q, il doit contenir %q", msg, want)
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
		t.Errorf("message = %q, il doit renvoyer vers `notion-seed init`", err.Error())
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
	}
	for _, tt := range tests {
		if got := versionAtLeast(tt.found, tt.min); got != tt.want {
			t.Errorf("versionAtLeast(%q, %q) = %v, want %v", tt.found, tt.min, got, tt.want)
		}
	}
}
