// SPDX-License-Identifier: GPL-3.0-or-later

package transport

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// fakeNtnDir compile le faux ntn dans un dossier temporaire propre à l'appel et
// retourne ce dossier, prêt à être mis en tête de PATH. La compilation est
// refaite à chaque appel : le cache de build de Go la rend quasi gratuite après
// la première.
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

// withFakeNtn met le faux ntn en tête du PATH et fixe le scénario, pour la
// durée du test.
func withFakeNtn(t *testing.T, scenario string) {
	t.Helper()
	dir := fakeNtnDir(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_NTN_SCENARIO", scenario)
}
