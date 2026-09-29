// SPDX-License-Identifier: GPL-3.0-or-later

package state

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The fingerprint describes what the state holds, not how the file is laid
// out: the file Save writes and the same file reindented by hand load to the
// same fingerprint.
func TestFingerprintIgnoresTheLayoutOfTheFile(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, sampleSnapshot()); err != nil {
		t.Fatal(err)
	}
	saved, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := Fingerprint(saved)
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(want) {
		t.Fatalf("Fingerprint() = %q, want 64 lowercase hex digits", want)
	}

	raw, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	flat := strings.Join(strings.Fields(string(raw)), " ")
	if err := os.WriteFile(Path(dir), []byte(flat), 0o644); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := Fingerprint(reloaded); got != want {
		t.Errorf("Fingerprint() = %s, want %s: only the layout changed", got, want)
	}
}

// A missing file and an empty state are the same starting point.
func TestFingerprintOfAMissingStateIsTheEmptyOne(t *testing.T) {
	missing, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := Fingerprint(missing), Fingerprint(&Snapshot{}); got != want {
		t.Errorf("Fingerprint(missing) = %s, want %s", got, want)
	}
	if got, want := Fingerprint(nil), Fingerprint(&Snapshot{}); got != want {
		t.Errorf("Fingerprint(nil) = %s, want %s", got, want)
	}
}

// Every change of an identity or of the applied snapshot changes the
// fingerprint: that is what a second apply between plan and apply leaves
// behind.
func TestFingerprintChangesWithTheState(t *testing.T) {
	base := Fingerprint(sampleSnapshot())
	for _, tc := range []struct {
		name string
		edit func(*Snapshot)
	}{
		{"workspace", func(s *Snapshot) { s.WorkspaceID = "other" }},
		{"database id", func(s *Snapshot) {
			db := s.Databases["tasks"]
			db.ID = "other"
			s.Databases["tasks"] = db
		}},
		{"option key", func(s *Snapshot) {
			db := s.Databases["tasks"]
			db.Properties["Statut"].Options[0].Key = "other"
		}},
		{"database added", func(s *Snapshot) { s.Databases["projects"] = Database{ID: "p"} }},
		{"database removed", func(s *Snapshot) { delete(s.Databases, "tasks") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := sampleSnapshot()
			tc.edit(s)
			if Fingerprint(s) == base {
				t.Errorf("Fingerprint() unchanged by %q", tc.name)
			}
		})
	}
}

// Fingerprint must not stamp the snapshot the way Save does: plan computes it
// on the snapshot apply will later write.
func TestFingerprintDoesNotModifyTheSnapshot(t *testing.T) {
	s := &Snapshot{Version: 0, WorkspaceID: "w"}
	Fingerprint(s)
	if s.Version != 0 {
		t.Errorf("Version = %d, want 0: Fingerprint modified its argument", s.Version)
	}
}
