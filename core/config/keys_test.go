// SPDX-License-Identifier: GPL-3.0-or-later

package config

import "testing"

func TestDeriveKey(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"Projects", "projects"},
		{"My Tasks", "my-tasks"},
		{"Rétrospectives", "retrospectives"},
		{"Suivi  des   projets", "suivi-des-projets"},
		{"Q1/Q2 Roadmap", "q1-q2-roadmap"},
		{"  Trailing  ", "trailing"},
		{"Déjà-vu", "deja-vu"},
		{"2026 Goals", "2026-goals"},
		{"---", "resource"},
		{"", "resource"},
	}
	for _, tt := range tests {
		if got := DeriveKey(tt.in); got != tt.want {
			t.Errorf("DeriveKey(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestResolveKeysKeepsExplicitKeys(t *testing.T) {
	in := []Database{
		{Key: "proj", Name: "Projects"},
		{Key: "tsk", Name: "Tasks"},
	}
	got := ResolveKeys(in, "Workspace")
	if got[0].Key != "proj" || got[1].Key != "tsk" {
		t.Errorf("explicit keys must be kept, got %q / %q", got[0].Key, got[1].Key)
	}
}

func TestResolveKeysDerivesMissingKeys(t *testing.T) {
	in := []Database{{Name: "My Projects"}}
	got := ResolveKeys(in, "Workspace")
	if got[0].Key != "my-projects" {
		t.Errorf("Key = %q, want %q", got[0].Key, "my-projects")
	}
}

// Step 2 of the strategy: prefix with the parent page's name.
func TestResolveKeysPrefixesWithParentOnCollision(t *testing.T) {
	in := []Database{
		{Name: "Tasks"},
		{Name: "Tasks"},
	}
	got := ResolveKeys(in, "Engineering")
	if got[0].Key != "tasks" {
		t.Errorf("first Key = %q, want %q", got[0].Key, "tasks")
	}
	if got[1].Key != "engineering-tasks" {
		t.Errorf("second Key = %q, want %q", got[1].Key, "engineering-tasks")
	}
}

// Step 3: numeric suffix when the prefix is not enough.
func TestResolveKeysFallsBackToNumericSuffix(t *testing.T) {
	in := []Database{
		{Name: "Tasks"},
		{Name: "Tasks"},
		{Name: "Tasks"},
	}
	got := ResolveKeys(in, "Engineering")
	want := []string{"tasks", "engineering-tasks", "engineering-tasks-2"}
	for i, w := range want {
		if got[i].Key != w {
			t.Errorf("Key[%d] = %q, want %q", i, got[i].Key, w)
		}
	}
}

// An explicit key must never be overwritten by another one's resolution.
func TestResolveKeysNeverOverwritesExplicitKey(t *testing.T) {
	in := []Database{
		{Name: "Tasks"},
		{Key: "tasks", Name: "Autre chose"},
	}
	got := ResolveKeys(in, "Engineering")
	if got[1].Key != "tasks" {
		t.Errorf("the explicit key was changed: %q", got[1].Key)
	}
	if got[0].Key == "tasks" {
		t.Errorf("the derived key collides with an explicit key: %q", got[0].Key)
	}
}

func TestResolveKeysDoesNotMutateInput(t *testing.T) {
	in := []Database{{Name: "Tasks"}}
	_ = ResolveKeys(in, "Engineering")
	if in[0].Key != "" {
		t.Errorf("the input was changed: Key = %q", in[0].Key)
	}
}
