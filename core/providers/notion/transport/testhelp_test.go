// SPDX-License-Identifier: GPL-3.0-or-later

package transport

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// fakeNtnDir builds the fake ntn in a temporary directory specific to the call
// and returns that directory, ready to be put at the front of PATH. The build
// is redone on every call: Go's build cache makes it nearly free after the
// first one.
func fakeNtnDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "ntn")
	cmd := exec.Command("go", "build", "-o", out, "../../../../testdata/fakentn")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fakentn: %v\n%s", err, b)
	}
	return dir
}

// withFakeNtn puts the fake ntn at the front of PATH and sets the scenario,
// for the duration of the test.
func withFakeNtn(t *testing.T, scenario string) {
	t.Helper()
	dir := fakeNtnDir(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_NTN_SCENARIO", scenario)
}
