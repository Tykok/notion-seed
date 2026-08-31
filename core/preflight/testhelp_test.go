package preflight

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func withFakeNtn(t *testing.T, scenario string) {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "ntn")
	cmd := exec.Command("go", "build", "-o", out, "../../testdata/fakentn")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fakentn: %v\n%s", err, b)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_NTN_SCENARIO", scenario)
}

// withEmptyPath simule un ntn absent.
func withEmptyPath(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}
