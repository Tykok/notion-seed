// SPDX-License-Identifier: GPL-3.0-or-later

package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sampleSnapshot() *Snapshot {
	return &Snapshot{
		Version:     Version,
		WorkspaceID: "33333333-3333-4333-8333-333333333333",
		Databases: map[string]Database{
			"tasks": {
				ID:           "1b2c3d4e",
				DataSourceID: "9f8e7d6c",
				Name:         "Tasks",
				Properties: map[string]Property{
					"Statut": {
						ID:   "abc",
						Type: "status",
						Options: []Option{
							{ID: "opt-1", Key: "todo", Name: "À faire", Group: "To-do"},
						},
					},
				},
			},
		},
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	dir := t.TempDir()
	want := sampleSnapshot()
	if err := Save(dir, want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	db, ok := got.Databases["tasks"]
	if !ok {
		t.Fatal("database tasks absente après aller-retour")
	}
	if db.ID != "1b2c3d4e" || db.DataSourceID != "9f8e7d6c" {
		t.Errorf("ids = %q / %q", db.ID, db.DataSourceID)
	}
	opt := db.Properties["Statut"].Options[0]
	if opt.Key != "todo" || opt.Name != "À faire" || opt.Group != "To-do" {
		t.Errorf("option = %+v", opt)
	}
	if got.WorkspaceID != want.WorkspaceID {
		t.Errorf("WorkspaceID = %q", got.WorkspaceID)
	}
}

func TestSaveIsDeterministic(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	if err := Save(dirA, sampleSnapshot()); err != nil {
		t.Fatalf("Save(A) error = %v", err)
	}
	if err := Save(dirB, sampleSnapshot()); err != nil {
		t.Fatalf("Save(B) error = %v", err)
	}
	a, _ := os.ReadFile(Path(dirA))
	b, _ := os.ReadFile(Path(dirB))
	if string(a) != string(b) {
		t.Errorf("deux écritures du même snapshot diffèrent :\n%s\n---\n%s", a, b)
	}
	if !strings.HasSuffix(string(a), "\n") {
		t.Error("le fichier ne se termine pas par un newline")
	}
}

func TestLoadMissingFileIsEmptyNotError(t *testing.T) {
	got, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load() error = %v, want nil — un state absent est le cas normal", err)
	}
	if len(got.Databases) != 0 {
		t.Errorf("Databases = %v, want vide", got.Databases)
	}
}

func TestLoadRejectsUnknownVersion(t *testing.T) {
	dir := t.TempDir()
	body := `{"version":99,"databases":{}}`
	if err := os.WriteFile(Path(dir), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load() error = nil, want une erreur sur version inconnue")
	}
	if !strings.Contains(err.Error(), "99") || !strings.Contains(err.Error(), "→") {
		t.Errorf("le message doit nommer la version lue et donner une sortie: %v", err)
	}
}

// Review Focus 1 : un state tronqué ou mal fusionné ne doit ni paniquer ni
// passer pour un state vide — ce dernier cas ferait tout recréer.
func TestLoadRejectsInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(Path(dir), []byte(`{"version":1,"datab`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load() error = nil, want une erreur sur JSON tronqué")
	}
	if !strings.Contains(err.Error(), FileName) {
		t.Errorf("le message doit nommer le fichier: %v", err)
	}
}

func TestSaveWritesAWorldReadableFile(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, sampleSnapshot()); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	// Le fichier est fait pour être versionné et relu : os.CreateTemp le crée
	// en 0600, ce qui le rendrait illisible pour tout le monde sauf son auteur.
	if perm := fi.Mode().Perm(); perm != 0o644 {
		t.Errorf("mode = %o, want 644", perm)
	}
}

// Review Focus 3 : l'écriture atomique doit laisser l'ancien fichier intact
// quand elle échoue.
func TestSaveKeepsPreviousFileWhenWriteFails(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, sampleSnapshot()); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(Path(dir))

	// Un dossier non inscriptible fait échouer la création du fichier
	// temporaire, sans toucher au fichier final.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skipf("chmod indisponible ici: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	other := sampleSnapshot()
	other.Databases["tasks"] = Database{ID: "écrasé"}
	if err := Save(dir, other); err == nil {
		t.Skip("le dossier reste inscriptible (root ?), test non significatif")
	}

	_ = os.Chmod(dir, 0o700)
	after, _ := os.ReadFile(Path(dir))
	if string(after) != string(before) {
		t.Error("l'ancien state a été abîmé par une écriture échouée")
	}
	if entries, _ := filepath.Glob(filepath.Join(dir, ".notion-seed.state.*")); len(entries) != 0 {
		t.Errorf("fichiers temporaires laissés derrière: %v", entries)
	}
}
