// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func withFakeNtn(t *testing.T, scenario string) {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "ntn")
	cmd := exec.Command("go", "build", "-o", out, "../testdata/fakentn")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fakentn: %v\n%s", err, b)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_NTN_SCENARIO", scenario)
}

func TestInitReportsMissingNtnWithInstallCommand(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"init"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want an error when ntn is missing")
	}
	if !strings.Contains(err.Error(), "npm i -g ntn") {
		t.Errorf("message = %q, it must give the install command", err.Error())
	}
}

func TestInitTellsUserToRunNtnLoginWhenNotAuthenticated(t *testing.T) {
	withFakeNtn(t, "version_no_auth")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"init"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want an error when ntn is not authenticated")
	}
	if !strings.Contains(err.Error(), "ntn login") {
		t.Errorf("message = %q, it must say to run `ntn login`", err.Error())
	}
	// init must not advise rerunning `notion-seed init` before saying to run
	// `ntn login`: the user just typed init.
	if strings.Index(err.Error(), "ntn login") > strings.Index(err.Error(), "notion-seed init") {
		t.Errorf("message = %q: `ntn login` must come before rerunning `notion-seed init`", err.Error())
	}
	// The cause is appended: without it, a network outage or an internal ntn
	// crash shows as "not authenticated", on the command whose only job is to
	// diagnose the environment.
	if !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("message = %q, it must append the cause reported by ntn", err.Error())
	}
}

func TestInitReportsWorkspaceWhenAuthenticated(t *testing.T) {
	withFakeNtn(t, "authenticated")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"init"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, out.String())
	}
	got := out.String()
	for _, want := range []string{"0.22.11", "Example Space", "bot@example.com"} {
		if !strings.Contains(got, want) {
			t.Errorf("output = %q, it must contain %q", got, want)
		}
	}
}
