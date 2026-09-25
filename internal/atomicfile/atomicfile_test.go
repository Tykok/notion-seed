// SPDX-License-Identifier: GPL-3.0-or-later

package atomicfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteCreatesTheFileAtTheGivenPermission(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := Write(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello\n" {
		t.Errorf("content = %q, want %q", got, "hello\n")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644", info.Mode().Perm())
	}
}

func TestWriteReplacesTheFileAtomicallyAndLeavesNoTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("new\n"), 0o644); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new\n" {
		t.Errorf("content = %q, want %q", got, "new\n")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("directory holds %d entries, want only out.txt: a temporary file stayed", len(entries))
	}
}

func TestWriteNamesTheMissingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	path := filepath.Join(dir, "out.txt")
	err := Write(path, []byte("x"), 0o644)
	if err == nil {
		t.Fatal("Write() error = nil, want a failure")
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("message = %q, want it to name %q", err.Error(), dir)
	}
}

// Review Focus: the atomic write must leave the previous file intact when it
// fails partway.
func TestWriteKeepsThePreviousFileWhenItFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := Write(path, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skipf("chmod unavailable here: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := Write(path, []byte("after"), 0o644); err == nil {
		t.Skip("the directory stays writable (root?), test not meaningful")
	}

	_ = os.Chmod(dir, 0o700)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "before" {
		t.Error("the previous file was damaged by a failed write")
	}
	entries, _ := filepath.Glob(filepath.Join(dir, ".out.txt.*"))
	if len(entries) != 0 {
		t.Errorf("temporary files left behind: %v", entries)
	}
}
