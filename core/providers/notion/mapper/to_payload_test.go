// SPDX-License-Identifier: GPL-3.0-or-later

package mapper

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/tykok/notion-seed/core/state"
)

const testParentPageID = "33333333-3333-4333-8333-333333333333"

// Le payload ne doit contenir QUE ce que la cible porte. Un group substitué ici
// serait une écriture que le plan n'a pas affichée — le défaut historique
// "To-do" produisait exactement ça.
func TestPropertyPayloadWritesOnlyDeclaredGroups(t *testing.T) {
	p := state.Property{
		Type: "status",
		Options: []state.Option{
			{Name: "À faire", Group: "To-do"},
			{Name: "Fait", Group: "Complete"},
		},
	}
	got, err := PropertyPayload(p)
	if err != nil {
		t.Fatalf("PropertyPayload() error = %v", err)
	}
	opts := got["status"].(map[string]any)["options"].([]map[string]any)
	if opts[0]["group"] != "To-do" || opts[1]["group"] != "Complete" {
		t.Errorf("groups = %v, want To-do puis Complete", opts)
	}
}

// Une option de status sans group ne peut plus arriver ici (le schéma la
// refuse), et le mapper ne doit surtout pas en inventer un : il transmet le
// vide, et l'absence de group devient visible au lieu d'être masquée.
func TestPropertyPayloadDoesNotInventAGroup(t *testing.T) {
	p := state.Property{
		Type:    "status",
		Options: []state.Option{{Name: "Orpheline"}},
	}
	got, err := PropertyPayload(p)
	if err != nil {
		t.Fatalf("PropertyPayload() error = %v", err)
	}
	opts := got["status"].(map[string]any)["options"].([]map[string]any)
	if g, present := opts[0]["group"]; present {
		t.Errorf("group = %v, want absent : le mapper ne choisit aucun groupe", g)
	}
}

// La key de configuration est une identité interne : l'API ne la connaît pas et
// ne doit jamais la recevoir.
func TestPropertyPayloadNeverSendsTheConfigKey(t *testing.T) {
	p := state.Property{
		Type:    "select",
		Options: []state.Option{{Key: "haute", Name: "Haute", Color: "red"}},
	}
	got, err := PropertyPayload(p)
	if err != nil {
		t.Fatalf("PropertyPayload() error = %v", err)
	}
	opts := got["select"].(map[string]any)["options"].([]map[string]any)
	if _, present := opts[0]["key"]; present {
		t.Errorf("options = %v, la key de config ne doit pas partir vers l'API", opts)
	}
	if opts[0]["color"] != "red" {
		t.Errorf("color = %v, want red", opts[0]["color"])
	}
}

// Une couleur absente du YAML n'est pas transmise : notion-seed ne choisit pas
// à la place de l'utilisateur, exactement comme pour le group.
func TestPropertyPayloadOmitsUndeclaredColor(t *testing.T) {
	p := state.Property{
		Type:    "multi_select",
		Options: []state.Option{{Name: "Urgent"}},
	}
	got, err := PropertyPayload(p)
	if err != nil {
		t.Fatalf("PropertyPayload() error = %v", err)
	}
	opts := got["multi_select"].(map[string]any)["options"].([]map[string]any)
	if _, present := opts[0]["color"]; present {
		t.Errorf("options = %v, want aucune couleur", opts)
	}
}

func TestPropertyPayloadCarriesNumberFormat(t *testing.T) {
	got, err := PropertyPayload(state.Property{Type: "number", Format: "euro"})
	if err != nil {
		t.Fatalf("PropertyPayload() error = %v", err)
	}
	if got["number"].(map[string]any)["format"] != "euro" {
		t.Errorf("payload = %v, want format euro", got)
	}
}

func TestPropertyPayloadRejectsUnsupportedType(t *testing.T) {
	_, err := PropertyPayload(state.Property{Type: "formula"})
	if !errors.Is(err, ErrUnsupportedType) {
		t.Errorf("error = %v, want ErrUnsupportedType", err)
	}
}

func TestDatabaseCreatePayloadCarriesParentTitleAndProperties(t *testing.T) {
	target := state.Database{
		Name:        "Tasks",
		Description: "Suivi",
		Icon:        "✅",
		Properties: map[string]state.Property{
			"Name":     {Type: "title"},
			"Estimate": {Type: "number", Format: "number"},
		},
	}
	raw, err := DatabaseCreatePayload("tasks", target, testParentPageID)
	if err != nil {
		t.Fatalf("DatabaseCreatePayload() error = %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	parent := body["parent"].(map[string]any)
	if parent["page_id"] != testParentPageID {
		t.Errorf("parent = %v", parent)
	}
	if parent["type"] != "page_id" {
		t.Errorf("parent.type = %v, want page_id", parent["type"])
	}
	// Depuis l'API 2025-09-03, le schéma vit dans le data source, transmis à la
	// création sous initial_data_source.
	props := body["initial_data_source"].(map[string]any)["properties"].(map[string]any)
	if _, ok := props["Estimate"]; !ok {
		t.Errorf("properties = %v, want Estimate", props)
	}
	if body["icon"].(map[string]any)["emoji"] != "✅" {
		t.Errorf("icon = %v", body["icon"])
	}
}

// Une description vide ne doit pas produire un champ vide : l'API l'écrirait.
func TestDatabaseCreatePayloadOmitsEmptyDescriptionAndIcon(t *testing.T) {
	target := state.Database{
		Name:       "Tasks",
		Properties: map[string]state.Property{"Name": {Type: "title"}},
	}
	raw, err := DatabaseCreatePayload("tasks", target, testParentPageID)
	if err != nil {
		t.Fatalf("DatabaseCreatePayload() error = %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if _, present := body["description"]; present {
		t.Error("description présente alors que la cible n'en porte pas")
	}
	if _, present := body["icon"]; present {
		t.Error("icon présent alors que la cible n'en porte pas")
	}
}

// Le message doit nommer la database ET la propriété : sans la key, l'erreur
// n'aide pas à trouver le fichier fautif.
func TestDatabaseCreatePayloadNamesKeyAndPropertyOnUnsupportedType(t *testing.T) {
	target := state.Database{
		Name:       "Tasks",
		Properties: map[string]state.Property{"Bizarre": {Type: "formula"}},
	}
	_, err := DatabaseCreatePayload("tasks", target, testParentPageID)
	if err == nil {
		t.Fatal("DatabaseCreatePayload() error = nil, want un refus de type")
	}
	for _, want := range []string{"tasks", "Bizarre", "formula", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, il doit contenir %q", err.Error(), want)
		}
	}
}

// Sans l'id, l'option existante serait appariée par nom côté API : un nom
// changé deviendrait un retrait suivi d'un ajout, et les lignes qui la
// portaient perdraient leur valeur. Une option neuve n'en a pas.
func TestOptionsPayloadCarriesExistingIDsAndOmitsThemForNewOnes(t *testing.T) {
	target := state.Database{Properties: map[string]state.Property{
		"Prio": {Type: "select", Options: []state.Option{
			{ID: "o-haute", Name: "Haute", Color: "red"},
			{Name: "Moyenne", Color: "orange"},
		}},
	}}

	body, err := DataSourceUpdatePayload("tasks", target, []string{"Prio"})
	if err != nil {
		t.Fatalf("DataSourceUpdatePayload() error = %v", err)
	}
	var got struct {
		Properties map[string]struct {
			Select struct {
				Options []map[string]any `json:"options"`
			} `json:"select"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("payload illisible: %v", err)
	}
	opts := got.Properties["Prio"].Select.Options
	if len(opts) != 2 {
		t.Fatalf("options = %d, want 2", len(opts))
	}
	if opts[0]["id"] != "o-haute" {
		t.Errorf("options[0][id] = %v, want o-haute — sans l'id, l'API détruit l'option", opts[0]["id"])
	}
	if _, present := opts[1]["id"]; present {
		t.Errorf("options[1] porte un id alors qu'elle est neuve : %v", opts[1])
	}
}

// Seules les propriétés du plan partent : en envoyer davantage écrirait des
// propriétés que le plan n'a pas montrées.
func TestDataSourceUpdatePayloadSendsOnlyTheNamedProperties(t *testing.T) {
	target := state.Database{Properties: map[string]state.Property{
		"Prio":  {Type: "select"},
		"Notes": {Type: "rich_text"},
	}}
	body, err := DataSourceUpdatePayload("tasks", target, []string{"Prio"})
	if err != nil {
		t.Fatalf("DataSourceUpdatePayload() error = %v", err)
	}
	if strings.Contains(string(body), "Notes") {
		t.Errorf("payload = %s, want sans Notes : seules les propriétés du plan partent", body)
	}
}

// La description est refusée par l'API sur le data source (mesuré le
// 2026-09-24) : seuls les champs nommés côté database partent, jamais plus.
func TestDatabaseUpdatePayloadSendsOnlyTheNamedFields(t *testing.T) {
	target := state.Database{Name: "Tâches", Description: "desc", Icon: "🟢"}
	body, err := DatabaseUpdatePayload("tasks", target, []string{"name", "icon"})
	if err != nil {
		t.Fatalf("DatabaseUpdatePayload() error = %v", err)
	}
	s := string(body)
	if !strings.Contains(s, "Tâches") || !strings.Contains(s, "🟢") {
		t.Errorf("payload = %s, want le titre et l'icône", s)
	}
	if strings.Contains(s, "description") {
		t.Errorf("payload = %s, want sans description : elle n'est pas dans le plan", s)
	}
}

// Review Focus #4 : un type non supporté doit nommer la database ET la
// propriété, et l'erreur doit remonter avant tout appel.
func TestDataSourceUpdatePayloadNamesTheUnsupportedProperty(t *testing.T) {
	target := state.Database{Properties: map[string]state.Property{
		"Lien": {Type: "relation"},
	}}
	_, err := DataSourceUpdatePayload("tasks", target, []string{"Lien"})
	if err == nil {
		t.Fatal("error = nil, want une erreur de type non supporté")
	}
	for _, want := range []string{"tasks", "Lien", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("erreur = %q, want contenant %q", err, want)
		}
	}
}
