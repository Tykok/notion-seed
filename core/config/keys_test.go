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
		t.Errorf("les key explicites doivent être conservées, got %q / %q", got[0].Key, got[1].Key)
	}
}

func TestResolveKeysDerivesMissingKeys(t *testing.T) {
	in := []Database{{Name: "My Projects"}}
	got := ResolveKeys(in, "Workspace")
	if got[0].Key != "my-projects" {
		t.Errorf("Key = %q, want %q", got[0].Key, "my-projects")
	}
}

// Étape 2 de la stratégie : préfixer par le nom de la page parente.
func TestResolveKeysPrefixesWithParentOnCollision(t *testing.T) {
	in := []Database{
		{Name: "Tasks"},
		{Name: "Tasks"},
	}
	got := ResolveKeys(in, "Engineering")
	if got[0].Key != "tasks" {
		t.Errorf("premier Key = %q, want %q", got[0].Key, "tasks")
	}
	if got[1].Key != "engineering-tasks" {
		t.Errorf("second Key = %q, want %q", got[1].Key, "engineering-tasks")
	}
}

// Étape 3 : suffixe numérique quand le préfixe ne suffit pas.
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

// Une key explicite ne doit jamais être écrasée par la résolution d'une autre.
func TestResolveKeysNeverOverwritesExplicitKey(t *testing.T) {
	in := []Database{
		{Name: "Tasks"},
		{Key: "tasks", Name: "Autre chose"},
	}
	got := ResolveKeys(in, "Engineering")
	if got[1].Key != "tasks" {
		t.Errorf("la key explicite a été modifiée: %q", got[1].Key)
	}
	if got[0].Key == "tasks" {
		t.Errorf("la key dérivée entre en collision avec une key explicite: %q", got[0].Key)
	}
}

func TestResolveKeysDoesNotMutateInput(t *testing.T) {
	in := []Database{{Name: "Tasks"}}
	_ = ResolveKeys(in, "Engineering")
	if in[0].Key != "" {
		t.Errorf("l'entrée a été modifiée: Key = %q", in[0].Key)
	}
}
