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
		t.Fatal("Execute() error = nil, want une erreur quand ntn est absent")
	}
	if !strings.Contains(err.Error(), "npm i -g ntn") {
		t.Errorf("message = %q, il doit donner la commande d'installation", err.Error())
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
		t.Fatal("Execute() error = nil, want une erreur quand ntn n'est pas authentifié")
	}
	if !strings.Contains(err.Error(), "ntn login") {
		t.Errorf("message = %q, il doit dire de lancer `ntn login`", err.Error())
	}
	// init ne doit pas conseiller de relancer `notion-seed init` avant d'avoir
	// dit de lancer `ntn login` : l'utilisateur vient de taper init.
	if strings.Index(err.Error(), "ntn login") > strings.Index(err.Error(), "notion-seed init") {
		t.Errorf("message = %q : `ntn login` doit venir avant la relance de `notion-seed init`", err.Error())
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
			t.Errorf("sortie = %q, elle doit contenir %q", got, want)
		}
	}
}
