// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
	"github.com/tykok/notion-seed/core/config"
	"github.com/tykok/notion-seed/core/planfile"
	"github.com/tykok/notion-seed/core/state"
)

// readPlanFile decodes a plan file the way apply will.
func readPlanFile(t *testing.T, path string) planfile.File {
	t.Helper()
	f, err := planfile.Decode([]byte(mustReadFile(t, path)), Version)
	if err != nil {
		t.Fatalf("Decode(%s) error = %v", path, err)
	}
	return f
}

// --out changes nothing to what plan prints, and the file carries what ties
// the plan to its starting point: version, workspace, fingerprints.
func TestPlanOutWritesThePlanWithoutChangingTheOutput(t *testing.T) {
	withFakeNtn(t, "authenticated")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": twoDatabases,
	})
	planPath := filepath.Join(t.TempDir(), "plan.out")

	without, err := runCmd(t, "plan", "--dir", dir)
	if err != nil {
		t.Fatalf("plan: %v\n%s", err, without)
	}
	with, err := runCmd(t, "plan", "--dir", dir, "--out", planPath)
	if err != nil {
		t.Fatalf("plan --out: %v\n%s", err, with)
	}
	if with != without {
		t.Errorf("--out changed the output:\n%s", lineDiff(without, with))
	}

	f := readPlanFile(t, planPath)
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := state.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if f.NotionSeed != Version {
		t.Errorf("notion_seed = %q, want %q", f.NotionSeed, Version)
	}
	if f.WorkspaceID != "33333333-3333-4333-8333-333333333333" {
		t.Errorf("workspace_id = %q, want the one ntn is authenticated on", f.WorkspaceID)
	}
	if f.ConfigSHA256 != config.Fingerprint(cfg) {
		t.Errorf("config_sha256 = %q, want %q", f.ConfigSHA256, config.Fingerprint(cfg))
	}
	if f.StateSHA256 != state.Fingerprint(snap) {
		t.Errorf("state_sha256 = %q, want %q", f.StateSHA256, state.Fingerprint(snap))
	}
	if f.CreatedAt.IsZero() {
		t.Error("created_at is empty")
	}
	// rendered is the plan as printed, without the preflight header.
	if !strings.HasSuffix(without, f.Rendered) || !strings.HasPrefix(f.Rendered, "Plan: 2 to add") {
		t.Errorf("rendered = %q, want the plan as printed", f.Rendered)
	}
	if len(f.Changes) != 2 || f.Changes[0].Resource != "database.projects" ||
		f.Changes[0].Kind != "create" || f.Changes[1].Resource != "database.tasks" {
		t.Errorf("changes = %+v, want the two creations", f.Changes)
	}
}

func TestDiffOutWritesThePlanToo(t *testing.T) {
	withFakeNtn(t, "authenticated")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": twoDatabases,
	})
	planPath := filepath.Join(t.TempDir(), "plan.out")

	if out, err := runCmd(t, "diff", "--dir", dir, "--out", planPath); err != nil {
		t.Fatalf("diff --out: %v\n%s", err, out)
	}
	if f := readPlanFile(t, planPath); len(f.Changes) != 2 {
		t.Errorf("changes = %+v, want the two creations", f.Changes)
	}
}

// Review Focus #5: the file is written AFTER --fail-on, whatever it decided.
// A CI that stops on --fail-on still has the plan to review — and the exit
// code is still --fail-on's.
func TestPlanOutIsWrittenWhenFailOnFails(t *testing.T) {
	dir := importThenDeclare(t, orphanYAML)
	planPath := filepath.Join(t.TempDir(), "plan.out")

	out, err := runCmd(t, "plan", "--dir", dir, "--out", planPath, "--fail-on=destructive")
	if err == nil {
		t.Fatalf("plan error = nil, want the --fail-on failure\n%s", out)
	}
	if !strings.Contains(err.Error(), "--fail-on") {
		t.Errorf("message = %q, want the --fail-on failure", err.Error())
	}
	f := readPlanFile(t, planPath)
	if len(f.Changes) != 1 || f.Changes[0].Kind != "destroy" {
		t.Fatalf("changes = %+v, want the destruction", f.Changes)
	}
	line := f.Changes[0].Details[0]
	if line.Bound != planfile.BoundExact || line.Count == nil || *line.Count != 3 {
		t.Errorf("line = %+v, want the 3 rows counted, exact", line)
	}
}

// A blocked plan is written too, and says so: apply will refuse it.
func TestPlanOutRecordsABlockedPlan(t *testing.T) {
	withFakeNtn(t, "authenticated_database_404")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":       tasksWorkspaceYAML,
		"databases/tasks.yaml": tasksConfigYAML,
		state.FileName:         tasksStateJSON,
	})
	planPath := filepath.Join(t.TempDir(), "plan.out")

	out, err := runCmd(t, "plan", "--dir", dir, "--out", planPath)
	if err == nil || !strings.Contains(err.Error(), "plan blocked") {
		t.Fatalf("plan error = %v, want the block\n%s", err, out)
	}
	if f := readPlanFile(t, planPath); !f.Blocked {
		t.Error("blocked = false, want true")
	}
}

// Offline, nothing was measured: a plan file would promise what nothing
// backs. Refused before anything, and nothing is written.
func TestPlanOutRefusesSkipPreflight(t *testing.T) {
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": twoDatabases,
	})
	planPath := filepath.Join(t.TempDir(), "plan.out")

	out, err := runCmd(t, "plan", "--dir", dir, "--skip-preflight", "--out", planPath)
	if err == nil {
		t.Fatalf("plan error = nil, want a refusal\n%s", out)
	}
	for _, want := range []string{"--out", "--skip-preflight", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, want %q", err.Error(), want)
		}
	}
	if strings.Contains(out, "Plan:") {
		t.Errorf("a plan was rendered before the refusal:\n%s", out)
	}
	if _, err := os.Stat(planPath); !os.IsNotExist(err) {
		t.Error("a plan file was written under --skip-preflight")
	}
}

// A file that cannot be written fails the command, after the plan was
// printed.
func TestPlanOutReportsAFileItCannotWrite(t *testing.T) {
	withFakeNtn(t, "authenticated")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": twoDatabases,
	})
	planPath := filepath.Join(t.TempDir(), "missing", "plan.out")

	out, err := runCmd(t, "plan", "--dir", dir, "--out", planPath)
	if err == nil {
		t.Fatalf("plan error = nil, want the write failure\n%s", out)
	}
	for _, want := range []string{planPath, "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, want %q", err.Error(), want)
		}
	}
	if !strings.Contains(out, "Plan: 2 to add") {
		t.Errorf("the plan was not printed before the failure:\n%s", out)
	}
}

// apply reads a plan file, it never writes one.
func TestApplyHasNoOutFlag(t *testing.T) {
	if f := newApplyCmd().Flags().Lookup("out"); f != nil {
		t.Errorf("apply exposes --out")
	}
}

// m2: the usage string must not carry a back-quoted span beyond the value
// name itself: pflag's UnquoteUsage takes the FIRST back-quoted span as the
// flag's value name. Back-quoting the whole "notion-seed apply <file>" would
// make --out's value name read as "notion-seed apply <file>" in --help.
func TestPlanOutUsageDoesNotWidenTheValueName(t *testing.T) {
	f := newPlanCmd().Flags().Lookup("out")
	if f == nil {
		t.Fatal("plan has no --out flag")
	}
	name, _ := pflag.UnquoteUsage(f)
	if name != "file" {
		t.Errorf("--out value name = %q, want %q (usage: %q)", name, "file", f.Usage)
	}
}
