// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tykok/notion-seed/core/state"
)

// reviewedOrphan sets up the destruction of the stateful scenario — 3 rows go
// to the trash —, writes its plan file, and logs every write from then on.
// It returns the config directory, the plan file and the write log.
func reviewedOrphan(t *testing.T) (dir, planPath, logPath string) {
	t.Helper()
	dir = importThenDeclare(t, orphanYAML)
	planPath = filepath.Join(t.TempDir(), "plan.out")
	if out, err := runCmd(t, "plan", "--dir", dir, "--out", planPath); err != nil {
		t.Fatalf("plan --out: %v\n%s", err, out)
	}
	logPath = withMutationLog(t)
	return dir, planPath, logPath
}

// assertRefusedBeforeAnything checks what a refusal of the reviewed plan
// guarantees: named, before the render and the confirmation, with zero writes
// to Notion and the state untouched.
func assertRefusedBeforeAnything(t *testing.T, dir, logPath, stateBefore, out string, err error, want ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("apply error = nil, want the refusal of the reviewed plan\n%s", out)
	}
	for _, w := range append([]string{"nothing was applied", "  → "}, want...) {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("message =\n%s\nwant %q", err.Error(), w)
		}
	}
	for _, never := range []string{"Plan:", "will be moved to the trash", "to confirm"} {
		if strings.Contains(out, never) {
			t.Errorf("output carries %q: the refusal must come before the render\n%s", never, out)
		}
	}
	if got := readMutationLog(t, logPath); got != "" {
		t.Errorf("writes = %q, want none", got)
	}
	if after := mustReadFile(t, filepath.Join(dir, state.FileName)); after != stateBefore {
		t.Error("the state was rewritten despite the refusal")
	}
}

// editPlanFile rewrites one field of a plan file, the way another version,
// another workspace or a hand edit would have written it.
func editPlanFile(t *testing.T, path, field string, value any) {
	t.Helper()
	var f map[string]any
	if err := json.Unmarshal([]byte(mustReadFile(t, path)), &f); err != nil {
		t.Fatal(err)
	}
	f[field] = value
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Important 2 (final review): the state fingerprint in metaOf is computed
// from prep.snap BEFORE cli/apply.go stamps prep.snap.WorkspaceID — an
// ordering guaranteed today only by a comment on that line. It is what lets a
// project with NO state yet be bootstrapped from a reviewed plan: a first
// apply from such a file must converge, and only afterwards does the state
// stop matching the file it was applied from, so a second apply of the SAME
// file is refused as any other apply going through in between would be.
func TestApplyOfAPlanFileBootstrapsAProjectWithNoState(t *testing.T) {
	withFakeNtn(t, "authenticated_create")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": oneDatabase,
	})
	planPath := filepath.Join(t.TempDir(), "plan.out")
	if out, err := runCmd(t, "plan", "--dir", dir, "--out", planPath); err != nil {
		t.Fatalf("plan --out: %v\n%s", err, out)
	}

	out, err := runCmd(t, "apply", planPath, "--dir", dir, "--auto-approve")
	if err != nil {
		t.Fatalf("first apply: %v\n%s", err, out)
	}
	snap, lerr := state.Load(dir)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if snap.WorkspaceID == "" {
		t.Fatal("the bootstrapped state carries no workspace_id")
	}

	out, err = runCmd(t, "apply", planPath, "--dir", dir, "--auto-approve")
	if err == nil || !strings.Contains(err.Error(), "state changed since the plan") {
		t.Fatalf("second apply error = %v, want the refusal of a changed state\n%s", err, out)
	}
}

// The flow of the spec: plan --out, then apply of the file, converges.
func TestApplyOfAReviewedPlanConverges(t *testing.T) {
	dir, planPath, logPath := reviewedOrphan(t)

	out, err := runCmd(t, "apply", planPath, "--dir", dir, "--auto-approve")
	if err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	if got := readMutationLog(t, logPath); got != trashLine {
		t.Errorf("writes = %q, want exactly %q", got, trashLine)
	}
	planOut, perr := runCmd(t, "plan", "--dir", dir)
	if perr != nil {
		t.Fatalf("plan: %v\n%s", perr, planOut)
	}
	if !strings.Contains(planOut, "No changes") {
		t.Errorf("the plan following the apply is not empty:\n%s", planOut)
	}
}

// P4: fewer rows than reviewed is covered by what was accepted. The render is
// the RECOMPUTED plan's: its figure is the one that goes out.
func TestApplyOfAReviewedPlanPassesWithALowerCount(t *testing.T) {
	dir, planPath, logPath := reviewedOrphan(t)
	t.Setenv("FAKE_NTN_QUERY_ROWS", "2")

	out, err := runCmd(t, "apply", planPath, "--dir", dir, "--auto-approve")
	if err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	if !strings.Contains(out, "2 row(s) go to the trash with it") {
		t.Errorf("the render is not the recomputed plan's:\n%s", out)
	}
	if got := readMutationLog(t, logPath); got != trashLine {
		t.Errorf("writes = %q, want exactly %q", got, trashLine)
	}
}

func TestApplyOfAReviewedPlanRefusesAHigherCount(t *testing.T) {
	dir, planPath, logPath := reviewedOrphan(t)
	before := mustReadFile(t, filepath.Join(dir, state.FileName))
	t.Setenv("FAKE_NTN_QUERY_ROWS", "4")

	out, err := runCmd(t, "apply", planPath, "--dir", dir, "--auto-approve")
	assertRefusedBeforeAnything(t, dir, logPath, before, out, err,
		"database.tasks: 3 rows reviewed, 4 rows now",
		"rerun `notion-seed plan --out` and have the new plan reviewed")
}

// Review Focus #2: a count that fails at apply time refuses — the figure no
// longer bounds anything — and the message says it is the count, and that
// rerunning apply may be enough.
func TestApplyOfAReviewedPlanRefusesAFailedCountAndSaysRerun(t *testing.T) {
	dir, planPath, logPath := reviewedOrphan(t)
	before := mustReadFile(t, filepath.Join(dir, state.FileName))
	t.Setenv("FAKE_NTN_QUERY_STATUS", "403")

	out, err := runCmd(t, "apply", planPath, "--dir", dir, "--auto-approve")
	assertRefusedBeforeAnything(t, dir, logPath, before, out, err,
		"3 rows reviewed, no figure now: its count did not succeed",
		"a count failed",
		"rerun the same command to apply "+planPath)
	// The cause of the failure is printed, as on any plan.
	if !strings.Contains(out, "count failed — ") {
		t.Errorf("the count failure is not reported:\n%s", out)
	}
}

// P7: the configuration changed MEANING since the plan — an acknowledgement
// was added. Refused, and named.
func TestApplyOfAReviewedPlanRefusesAChangedConfiguration(t *testing.T) {
	dir, planPath, logPath := reviewedOrphan(t)
	before := mustReadFile(t, filepath.Join(dir, state.FileName))
	if err := os.WriteFile(filepath.Join(dir, "workspace.yaml"),
		[]byte(workspaceYAML+"lifecycle:\n  acknowledge_destroy:\n    - database.tasks\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runCmd(t, "apply", planPath, "--dir", dir, "--auto-approve")
	assertRefusedBeforeAnything(t, dir, logPath, before, out, err,
		"configuration changed since the plan")
}

// P7: a comment is not a change of meaning. The plan still applies.
func TestApplyOfAReviewedPlanIgnoresAComment(t *testing.T) {
	dir, planPath, logPath := reviewedOrphan(t)
	if err := os.WriteFile(filepath.Join(dir, "databases", "all.yaml"),
		[]byte("# every database was removed on purpose\n"+orphanYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	if out, err := runCmd(t, "apply", planPath, "--dir", dir, "--auto-approve"); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	if got := readMutationLog(t, logPath); got != trashLine {
		t.Errorf("writes = %q, want exactly %q", got, trashLine)
	}
}

// Another apply went through between the plan and this one, or its state was
// not committed: the plan describes a starting point that no longer exists.
func TestApplyOfAReviewedPlanRefusesAChangedState(t *testing.T) {
	dir, planPath, logPath := reviewedOrphan(t)
	snap, err := state.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	db := snap.Databases["tasks"]
	db.Description = "edited since the plan"
	snap.Databases["tasks"] = db
	if err := state.Save(dir, snap); err != nil {
		t.Fatal(err)
	}
	before := mustReadFile(t, filepath.Join(dir, state.FileName))

	out, err := runCmd(t, "apply", planPath, "--dir", dir, "--auto-approve")
	assertRefusedBeforeAnything(t, dir, logPath, before, out, err,
		"state changed since the plan")
}

// Review Focus #1: a plan file from another notion-seed version is refused,
// whatever it holds.
func TestApplyOfAReviewedPlanRefusesAnotherVersion(t *testing.T) {
	dir, planPath, logPath := reviewedOrphan(t)
	before := mustReadFile(t, filepath.Join(dir, state.FileName))
	editPlanFile(t, planPath, "notion_seed", "0.0.1")

	out, err := runCmd(t, "apply", planPath, "--dir", dir, "--auto-approve")
	assertRefusedBeforeAnything(t, dir, logPath, before, out, err,
		"notion-seed 0.0.1", "notion-seed "+Version, "rerun `notion-seed plan --out` with this version")
}

func TestApplyOfAReviewedPlanRefusesAnotherWorkspace(t *testing.T) {
	dir, planPath, logPath := reviewedOrphan(t)
	before := mustReadFile(t, filepath.Join(dir, state.FileName))
	editPlanFile(t, planPath, "workspace_id", "99999999-9999-4999-8999-999999999999")

	out, err := runCmd(t, "apply", planPath, "--dir", dir, "--auto-approve")
	assertRefusedBeforeAnything(t, dir, logPath, before, out, err,
		"workspace changed since the plan")
}

func TestApplyOfAReviewedPlanRefusesAnUnknownFormat(t *testing.T) {
	dir, planPath, logPath := reviewedOrphan(t)
	before := mustReadFile(t, filepath.Join(dir, state.FileName))
	editPlanFile(t, planPath, "format", 2)

	out, err := runCmd(t, "apply", planPath, "--dir", dir, "--auto-approve")
	assertRefusedBeforeAnything(t, dir, logPath, before, out, err, planPath, "format 2")
}

func TestApplyOfAMissingPlanFileWritesNothing(t *testing.T) {
	dir, _, logPath := reviewedOrphan(t)
	before := mustReadFile(t, filepath.Join(dir, state.FileName))
	missing := filepath.Join(t.TempDir(), "nope.out")

	out, err := runCmd(t, "apply", missing, "--dir", dir, "--auto-approve")
	assertRefusedBeforeAnything(t, dir, logPath, before, out, err, missing)
}

// Review Focus #5: the file written by a plan that failed on --fail-on is the
// file to review; applying it once reviewed converges. And a CI that keeps
// --fail-on on apply still never writes it.
func TestApplyOfAPlanWrittenByAFailedFailOn(t *testing.T) {
	dir := importThenDeclare(t, orphanYAML)
	planPath := filepath.Join(t.TempDir(), "plan.out")
	if _, err := runCmd(t, "plan", "--dir", dir, "--out", planPath, "--fail-on=destructive"); err == nil {
		t.Fatal("plan error = nil, want the --fail-on failure")
	}
	logPath := withMutationLog(t)

	out, err := runCmd(t, "apply", planPath, "--dir", dir, "--auto-approve", "--fail-on=destructive")
	if err == nil || !strings.Contains(err.Error(), "--fail-on") {
		t.Fatalf("apply error = %v, want the --fail-on refusal\n%s", err, out)
	}
	if got := readMutationLog(t, logPath); got != "" {
		t.Errorf("writes = %q, want none under --fail-on", got)
	}

	if out, err := runCmd(t, "apply", planPath, "--dir", dir, "--auto-approve"); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	if got := readMutationLog(t, logPath); got != trashLine {
		t.Errorf("writes = %q, want exactly %q", got, trashLine)
	}
}

// Review Focus #3: a resource withheld in both plans stays withheld, for the
// same reason — not a drift. apply goes on to its usual verdict: nothing
// written, non-zero exit because the plan does not converge.
func TestApplyOfAReviewedPlanKeepsAWithheldResourceWithheld(t *testing.T) {
	dir := importThenDeclare(t, tasksWithRenamedOption)
	planPath := filepath.Join(t.TempDir(), "plan.out")
	if out, err := runCmd(t, "plan", "--dir", dir, "--out", planPath); err != nil {
		t.Fatalf("plan --out: %v\n%s", err, out)
	}
	logPath := withMutationLog(t)

	out, err := runCmd(t, "apply", planPath, "--dir", dir, "--auto-approve")
	if err == nil {
		t.Fatalf("apply error = nil, want the non-convergence\n%s", out)
	}
	if !strings.Contains(err.Error(), "did not converge") {
		t.Errorf("message = %q, want the non-convergence, not a refusal of the reviewed plan", err.Error())
	}
	if !strings.Contains(out, "Withheld — migration required") {
		t.Errorf("the withheld resource is not rendered:\n%s", out)
	}
	if got := readMutationLog(t, logPath); got != "" {
		t.Errorf("writes = %q, want none", got)
	}
}

// Review Focus #4: an empty plan freezes into a file with no change; applying
// it does nothing and exits 0.
func TestApplyOfAnEmptyReviewedPlanDoesNothing(t *testing.T) {
	dir := importThenDeclare(t, tasksWithStatus)
	planPath := filepath.Join(t.TempDir(), "plan.out")
	if out, err := runCmd(t, "plan", "--dir", dir, "--out", planPath); err != nil {
		t.Fatalf("plan --out: %v\n%s", err, out)
	}
	if f := readPlanFile(t, planPath); len(f.Changes) != 0 {
		t.Fatalf("changes = %+v, want none", f.Changes)
	}
	logPath := withMutationLog(t)
	before := mustReadFile(t, filepath.Join(dir, state.FileName))

	out, err := runCmd(t, "apply", planPath, "--dir", dir, "--auto-approve")
	if err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	if !strings.Contains(out, "No changes") {
		t.Errorf("output:\n%s", out)
	}
	if got := readMutationLog(t, logPath); got != "" {
		t.Errorf("writes = %q, want none", got)
	}
	if after := mustReadFile(t, filepath.Join(dir, state.FileName)); after != before {
		t.Error("the state was rewritten on an empty plan")
	}
}

// Without a file, apply does not change: two arguments are refused by cobra,
// and none keeps the historical behaviour (covered by the rest of the suite).
func TestApplyTakesAtMostOnePlanFile(t *testing.T) {
	_, err := runCmd(t, "apply", "a.out", "b.out")
	if err == nil || !strings.Contains(err.Error(), "accepts at most 1 arg") {
		t.Errorf("apply error = %v, want cobra's refusal of a second argument", err)
	}
}

// withCallLog logs every fakentn invocation from then on, reads included, and
// returns the log.
func withCallLog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fakentn-calls.log")
	t.Setenv("FAKE_NTN_CALL_LOG", path)
	return path
}

// A plan file that cannot be applied whatever Notion holds — missing,
// unreadable, of an unknown format, from another version — is refused before
// the first call to ntn: no network is needed to know it, and none is spent.
func TestApplyOfAnInapplicablePlanFileCallsNothing(t *testing.T) {
	for name, spoil := range map[string]func(t *testing.T, path string) string{
		"missing": func(t *testing.T, _ string) string {
			return filepath.Join(t.TempDir(), "nope.out")
		},
		"unreadable": func(t *testing.T, path string) string {
			if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
				t.Fatal(err)
			}
			return path
		},
		"unknown format": func(t *testing.T, path string) string {
			editPlanFile(t, path, "format", 2)
			return path
		},
		"another version": func(t *testing.T, path string) string {
			editPlanFile(t, path, "notion_seed", "0.0.1")
			return path
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir, planPath, logPath := reviewedOrphan(t)
			before := mustReadFile(t, filepath.Join(dir, state.FileName))
			planPath = spoil(t, planPath)
			calls := withCallLog(t)

			out, err := runCmd(t, "apply", planPath, "--dir", dir, "--auto-approve")
			assertRefusedBeforeAnything(t, dir, logPath, before, out, err, planPath)
			if got := readMutationLog(t, calls); got != "" {
				t.Errorf("ntn calls = %q, want none: the file is refused before the network", got)
			}
		})
	}
}

// A drift found online still refuses; the call log proves the check did go
// through the network — the fail-fast test above is not vacuous.
func TestCallLogRecordsTheCallsOfAnApply(t *testing.T) {
	dir, planPath, _ := reviewedOrphan(t)
	calls := withCallLog(t)
	t.Setenv("FAKE_NTN_QUERY_ROWS", "4")

	if _, err := runCmd(t, "apply", planPath, "--dir", dir, "--auto-approve"); err == nil {
		t.Fatal("apply error = nil, want the refusal of a higher count")
	}
	if got := readMutationLog(t, calls); !strings.Contains(got, "/query") {
		t.Errorf("ntn calls = %q, want the count among them", got)
	}
}

// A plan file does not open an offline apply: --skip-preflight is refused
// with a file as without one, before the file is even read.
func TestApplyOfAPlanFileRefusesSkipPreflight(t *testing.T) {
	dir, planPath, logPath := reviewedOrphan(t)
	calls := withCallLog(t)

	out, err := runCmd(t, "apply", planPath, "--dir", dir, "--skip-preflight", "--auto-approve")
	if err == nil || !strings.Contains(err.Error(), "--skip-preflight") {
		t.Fatalf("apply error = %v, want the refusal of --skip-preflight\n%s", err, out)
	}
	if got := readMutationLog(t, logPath); got != "" {
		t.Errorf("writes = %q, want none", got)
	}
	if got := readMutationLog(t, calls); got != "" {
		t.Errorf("ntn calls = %q, want none", got)
	}
}
