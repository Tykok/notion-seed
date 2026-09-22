// SPDX-License-Identifier: GPL-3.0-or-later

package mapper

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/tykok/notion-seed/core/config"
)

func TestDatabaseCreatePayloadPutsPropertiesUnderInitialDataSource(t *testing.T) {
	db := config.Database{
		Key:         "projects",
		Name:        "Projects",
		Description: "Suivi des projets internes",
		Icon:        "🚀",
		Properties: map[string]config.Property{
			"Name": {Type: "title"},
		},
	}

	raw, err := DatabaseCreatePayload(db, "44444444-4444-4444-8444-444444444444")
	if err != nil {
		t.Fatalf("DatabaseCreatePayload() error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("payload illisible: %v", err)
	}

	parent, ok := got["parent"].(map[string]any)
	if !ok || parent["type"] != "page_id" ||
		parent["page_id"] != "44444444-4444-4444-8444-444444444444" {
		t.Errorf("parent = %v", got["parent"])
	}
	ids, ok := got["initial_data_source"].(map[string]any)
	if !ok {
		t.Fatalf("initial_data_source absent: %v", got)
	}
	props, ok := ids["properties"].(map[string]any)
	if !ok || props["Name"] == nil {
		t.Errorf("initial_data_source.properties = %v", ids["properties"])
	}
	if got["properties"] != nil {
		t.Error("les propriétés ne doivent pas être à la racine : depuis 2025-09-03 elles vivent dans le data source")
	}
}

func TestPropertyPayloadSimpleTypes(t *testing.T) {
	tests := []struct {
		name string
		in   config.Property
		want string // clé de configuration attendue dans le payload
	}{
		{"title", config.Property{Type: "title"}, "title"},
		{"rich_text", config.Property{Type: "rich_text"}, "rich_text"},
		{"url", config.Property{Type: "url"}, "url"},
		{"people", config.Property{Type: "people"}, "people"},
		{"date", config.Property{Type: "date"}, "date"},
		{"checkbox", config.Property{Type: "checkbox"}, "checkbox"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := PropertyPayload(tt.in)
			if err != nil {
				t.Fatalf("PropertyPayload() error = %v", err)
			}
			if _, ok := got[tt.want]; !ok {
				t.Errorf("payload = %v, want une clé %q", got, tt.want)
			}
		})
	}
}

func TestPropertyPayloadNumberCarriesFormat(t *testing.T) {
	got, err := PropertyPayload(config.Property{Type: "number", Format: "euro"})
	if err != nil {
		t.Fatalf("PropertyPayload() error = %v", err)
	}
	num, ok := got["number"].(map[string]any)
	if !ok || num["format"] != "euro" {
		t.Errorf("number = %v, want format euro", got["number"])
	}
}

func TestPropertyPayloadNumberWithoutFormatOmitsIt(t *testing.T) {
	got, err := PropertyPayload(config.Property{Type: "number"})
	if err != nil {
		t.Fatalf("PropertyPayload() error = %v", err)
	}
	num, ok := got["number"].(map[string]any)
	if !ok {
		t.Fatalf("number = %v", got["number"])
	}
	if _, present := num["format"]; present {
		t.Error("format absent de la config ne doit pas être envoyé")
	}
}

func TestPropertyPayloadSelectSendsFullOptionList(t *testing.T) {
	got, err := PropertyPayload(config.Property{
		Type: "select",
		Options: []config.Option{
			{Key: "low", Name: "Low", Color: "gray"},
			{Key: "high", Name: "High", Color: "red"},
		},
	})
	if err != nil {
		t.Fatalf("PropertyPayload() error = %v", err)
	}
	sel, ok := got["select"].(map[string]any)
	if !ok {
		t.Fatalf("select = %v", got["select"])
	}
	opts, ok := sel["options"].([]map[string]any)
	if !ok || len(opts) != 2 {
		t.Fatalf("options = %v, want 2 entrées", sel["options"])
	}
	if opts[0]["name"] != "Low" || opts[0]["color"] != "gray" {
		t.Errorf("options[0] = %v", opts[0])
	}
	// La key est interne à notion-seed : elle ne doit jamais partir vers l'API.
	if _, leaked := opts[0]["key"]; leaked {
		t.Error("la key de config a fuité dans le payload API")
	}
}

// Règle issue du spike : omettre `group` fait retomber toutes les options dans
// le premier groupe, silencieusement.
func TestPropertyPayloadStatusAlwaysSendsGroupPerOption(t *testing.T) {
	got, err := PropertyPayload(config.Property{
		Type: "status",
		Options: []config.Option{
			{Key: "todo", Name: "To-Do", Color: "gray", Group: "To-do"},
			{Key: "building", Name: "Building", Color: "blue", Group: "In progress"},
		},
	})
	if err != nil {
		t.Fatalf("PropertyPayload() error = %v", err)
	}
	st, _ := got["status"].(map[string]any)
	opts, ok := st["options"].([]map[string]any)
	if !ok || len(opts) != 2 {
		t.Fatalf("options = %v", st["options"])
	}
	for i, o := range opts {
		if o["group"] == nil || o["group"] == "" {
			t.Errorf("options[%d] n'a pas de group: %v", i, o)
		}
	}
	// Pas de champ groups à la racine : l'API ne l'accepte pas en modification,
	// et le groupe se pilote par option.
	if _, present := st["groups"]; present {
		t.Error("le payload ne doit pas porter de champ `groups`")
	}
}

func TestPropertyPayloadStatusDefaultsGroupWhenAbsent(t *testing.T) {
	got, err := PropertyPayload(config.Property{
		Type:    "status",
		Options: []config.Option{{Key: "todo", Name: "To-Do"}},
	})
	if err != nil {
		t.Fatalf("PropertyPayload() error = %v", err)
	}
	st, _ := got["status"].(map[string]any)
	opts, _ := st["options"].([]map[string]any)
	if opts[0]["group"] != "To-do" {
		t.Errorf("group = %v, want \"To-do\" par défaut", opts[0]["group"])
	}
}

func TestPropertyPayloadRejectsUnsupportedType(t *testing.T) {
	_, err := PropertyPayload(config.Property{Type: "formula"})
	if !errors.Is(err, ErrUnsupportedType) {
		t.Fatalf("error = %v, want ErrUnsupportedType", err)
	}
}

// Le déterminisme de la sortie est une contrainte liante, et le payload ne le
// tient que parce qu'encoding/json trie les clés de map. Rien ne l'épinglait :
// passer un jour à une construction ordonnée à la main (ou à un encodeur qui
// préserve l'ordre d'insertion) rendrait deux plans successifs différents sur
// une config identique.
func TestDatabaseCreatePayloadIsDeterministic(t *testing.T) {
	db := config.Database{
		Key:         "projects",
		Name:        "Projects",
		Description: "Suivi",
		Properties: map[string]config.Property{
			"Name":     {Type: "title"},
			"Estimate": {Type: "number", Format: "number"},
			"Owner":    {Type: "people"},
			"Statut": {Type: "status", Options: []config.Option{
				{Name: "À faire", Group: "To-do"},
				{Name: "Fini", Group: "Complete"},
			}},
			"Tags": {Type: "multi_select", Options: []config.Option{
				{Name: "a"}, {Name: "b"},
			}},
		},
	}

	first, err := DatabaseCreatePayload(db, "44444444-4444-4444-8444-444444444444")
	if err != nil {
		t.Fatalf("DatabaseCreatePayload() error = %v", err)
	}
	// Plusieurs itérations : l'ordre d'itération d'une map Go est randomisé à
	// chaque parcours, donc une seule comparaison pourrait passer par chance.
	for i := 0; i < 20; i++ {
		again, err := DatabaseCreatePayload(db, "44444444-4444-4444-8444-444444444444")
		if err != nil {
			t.Fatalf("DatabaseCreatePayload() error = %v", err)
		}
		if !bytes.Equal(first, again) {
			t.Fatalf("payload non déterministe à l'itération %d:\n  %s\n  %s", i, first, again)
		}
	}
}
