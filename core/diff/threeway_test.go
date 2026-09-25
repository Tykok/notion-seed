// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/tykok/notion-seed/core/change"
	"github.com/tykok/notion-seed/core/providers/notion/resources"
	"github.com/tykok/notion-seed/core/state"
)

// A creation must announce its OPTIONS, not only its properties. Without it,
// apply writes options — with their color and group — the plan never showed.
func TestCompareDatabaseListsOptionsOnCreation(t *testing.T) {
	desired := state.Database{
		Name: "Tasks",
		Properties: map[string]state.Property{
			"Statut": {Type: "status", Options: []state.Option{
				{Key: "todo", Name: "À faire", Color: "blue", Group: "To-do"},
			}},
		},
	}
	res := CompareDatabase("tasks", &desired, nil, nil)

	var lines []string
	for _, d := range res.Changeset.Details {
		lines = append(lines, d.Op+" "+d.Target+" — "+d.Note)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{`option "À faire"`, "color blue", "group To-do"} {
		if !strings.Contains(joined, want) {
			t.Errorf("details =\n%s\nmissing %q", joined, want)
		}
	}
}

// A creation ALSO writes the name, description and icon: they must therefore
// be announced. Omitting them was the unfixed half of the same flaw as the
// options — writing what the plan did not show.
func TestCompareDatabaseAnnouncesNameDescriptionAndIconOnCreation(t *testing.T) {
	desired := state.Database{
		Name:        "Notes de réunion",
		Description: "Toutes les notes",
		Icon:        "📝",
		Properties:  map[string]state.Property{"Titre": {Type: "title"}},
	}
	res := CompareDatabase("notes", &desired, nil, nil)

	var lines []string
	for _, d := range res.Changeset.Details {
		lines = append(lines, d.Op+" "+d.Target)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		`name "Notes de réunion"`,
		`description "Toutes les notes"`,
		`icon "📝"`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("details =\n%s\nmissing %q", joined, want)
		}
	}
	// The name comes first: it is what the user looks for first.
	if !strings.HasPrefix(joined, `+ name "Notes de réunion"`) {
		t.Errorf("the first line must be the name, got:\n%s", joined)
	}
}

// What the YAML does not declare is not written, so it is not announced: same
// non-emptiness guard as everywhere else.
func TestCompareDatabaseOmitsUndeclaredFieldsOnCreation(t *testing.T) {
	desired := state.Database{
		Name:       "Notes",
		Properties: map[string]state.Property{"Titre": {Type: "title"}},
	}
	res := CompareDatabase("notes", &desired, nil, nil)
	for _, d := range res.Changeset.Details {
		if strings.HasPrefix(d.Target, "description") || strings.HasPrefix(d.Target, "icon") {
			t.Errorf("unexpected line %q: the YAML declares neither description nor icon", d.Target)
		}
	}
}

// The resolved target IS what will be written. As long as apply only makes
// creations, it matches the desired way, and that is precisely the invariant
// that makes plan and apply inseparable.
func TestCompareDatabaseResolvesTargetOnCreation(t *testing.T) {
	desired := state.Database{
		Name:       "Tasks",
		Properties: map[string]state.Property{"Name": {Type: "title"}},
	}
	res := CompareDatabase("tasks", &desired, nil, nil)
	if res.Target == nil {
		t.Fatal("Target = nil, want the resolved target of the creation")
	}
	if !reflect.DeepEqual(*res.Target, desired) {
		t.Errorf("Target = %+v, want %+v", *res.Target, desired)
	}
}

// db builds a pivot database with a single property, to lighten the table.
func db(propName string, p state.Property) *state.Database {
	return &state.Database{
		Name:       "Tasks",
		Properties: map[string]state.Property{propName: p},
	}
}

func status(opts ...state.Option) state.Property {
	return state.Property{ID: "p1", Type: "status", Options: opts}
}

func sel(opts ...state.Option) state.Property {
	return state.Property{ID: "p1", Type: "select", Options: opts}
}

func multiSel(opts ...state.Option) state.Property {
	return state.Property{ID: "p1", Type: "multi_select", Options: opts}
}

// fixtureTasks returns a triple that produces at least one line of each shape:
// a database field, a new property, a type change, a new option, a removed
// option. The tests of this file use it rather than each building a triple.
func fixtureTasks() (desired, applied, actual state.Database) {
	actual = state.Database{
		ID: "db-1", DataSourceID: "ds-1",
		Name: "Tasks", Description: "ancienne", Icon: "🔵",
		Properties: map[string]state.Property{
			"Name":  {ID: "title", Type: "title"},
			"Notes": {ID: "n1", Type: "rich_text"},
			"Libre": {ID: "l1", Type: "rich_text"},
			"Prio": {ID: "p1", Type: "select", Options: []state.Option{
				{ID: "o-haute", Name: "Haute", Color: "red"},
				{ID: "o-basse", Name: "Basse", Color: "blue"},
			}},
		},
	}
	applied = state.Database{
		ID: "db-1", DataSourceID: "ds-1",
		Name: "Tasks", Description: "ancienne", Icon: "🔵",
		Properties: map[string]state.Property{
			"Name":  {ID: "title", Type: "title"},
			"Notes": {ID: "n1", Type: "rich_text"},
			"Libre": {ID: "l1", Type: "rich_text"},
			"Prio": {ID: "p1", Type: "select", Options: []state.Option{
				{ID: "o-haute", Key: "haute", Name: "Haute", Color: "red"},
				{ID: "o-basse", Key: "basse", Name: "Basse", Color: "blue"},
			}},
		},
	}
	desired = state.Database{
		Name: "Tâches", Description: "nouvelle", Icon: "🟢",
		Properties: map[string]state.Property{
			"Name":  {Type: "title"},
			"Notes": {Type: "number", Format: "number"},
			"Neuve": {Type: "rich_text"},
			"Prio": {Type: "select", Options: []state.Option{
				{Key: "haute", Name: "Haute", Color: "red"},
				{Key: "moyenne", Name: "Moyenne", Color: "orange"},
			}},
		},
	}
	return desired, applied, actual
}

func TestUpdateTargetCarriesRemoteOptionIDs(t *testing.T) {
	desired, applied, actual := fixtureTasks()
	res := CompareDatabase("tasks", &desired, &applied, &actual)
	if res.Target == nil {
		t.Fatal("Target = nil on an update")
	}

	prio := res.Target.Properties["Prio"]
	if len(prio.Options) != 2 {
		t.Fatalf("options = %d, want 2", len(prio.Options))
	}
	// "Haute" exists in Notion: the target MUST carry its id, otherwise the
	// PATCH destroys and re-creates it, and the rows that held it lose their
	// value.
	if prio.Options[0].Name != "Haute" || prio.Options[0].ID != "o-haute" {
		t.Errorf("option[0] = %+v, want Haute / o-haute", prio.Options[0])
	}
	// "Moyenne" is new: no id, the API creates one for it.
	if prio.Options[1].Name != "Moyenne" || prio.Options[1].ID != "" {
		t.Errorf("option[1] = %+v, want Moyenne without an id", prio.Options[1])
	}
	// "Basse" is no longer claimed: its absence from the target IS what
	// destroys it, and the plan already announces it with a "-" line.
	for _, o := range prio.Options {
		if o.Name == "Basse" {
			t.Error("option Basse is in the target while the YAML no longer claims it")
		}
	}
}

// An option rename — same key, different name — cannot be expressed in the
// API: the PATCH returns 200 without changing anything. Writing would record
// in the state a name Notion does not hold, so the WHOLE resource must be
// withheld rather than half written.
func TestMigrationWithholdsTheWholeResource(t *testing.T) {
	desired, applied, actual := fixtureTasks()
	desired.Properties["Prio"] = state.Property{Type: "select", Options: []state.Option{
		{Key: "haute", Name: "Très haute", Color: "red"},
	}}
	res := CompareDatabase("tasks", &desired, &applied, &actual)

	if res.Target != nil {
		t.Error("Target non-nil on a withheld resource: apply would write it")
	}
	if res.Withheld == "" {
		t.Fatal("Withheld empty: a nil target with no reason leaves the user guessing")
	}
	if !strings.Contains(res.Withheld, "  → ") {
		t.Errorf("Withheld = %q, want a corrective action introduced by \"  → \"", res.Withheld)
	}
}

// The mirror case: a resource with no migration must never be withheld, and
// Target stays the write permission apply expects.
func TestAResourceWithoutMigrationIsNotWithheld(t *testing.T) {
	desired, applied, actual := fixtureTasks()
	res := CompareDatabase("tasks", &desired, &applied, &actual)
	if res.Withheld != "" {
		t.Errorf("Withheld = %q, want empty", res.Withheld)
	}
	if res.Target == nil {
		t.Error("Target = nil while nothing withholds the resource")
	}
}

func TestUpdateTargetKeepsUndeclaredPropertiesOut(t *testing.T) {
	desired, applied, actual := fixtureTasks()
	res := CompareDatabase("tasks", &desired, &applied, &actual)
	if _, present := res.Target.Properties["Libre"]; present {
		t.Error("the unmanaged property is in the target: it would go in a payload")
	}
}

func TestUpdateTargetDoesNotOverwriteUndeclaredFields(t *testing.T) {
	desired, applied, actual := fixtureTasks()
	desired.Name, desired.Description, desired.Icon = "", "", ""
	res := CompareDatabase("tasks", &desired, &applied, &actual)
	if res.Target.Name != "Tasks" || res.Target.Description != "ancienne" || res.Target.Icon != "🔵" {
		t.Errorf("target = %q / %q / %q, want the actual values intact",
			res.Target.Name, res.Target.Description, res.Target.Icon)
	}
}

// Measured on 2026-09-24: a type change RE-CREATES the options, and the API
// ignores the ids sent (sent 6993c61f/36af0279, returned fba2569a/f62f86cf).
// Carrying them would suggest an identity continuity that does not exist.
func TestUpdateTargetDropsOptionIDsOnTypeChange(t *testing.T) {
	desired, applied, actual := fixtureTasks()
	desired.Properties["Prio"] = state.Property{Type: "multi_select", Options: []state.Option{
		{Key: "haute", Name: "Haute", Color: "red"},
	}}
	res := CompareDatabase("tasks", &desired, &applied, &actual)

	prio := res.Target.Properties["Prio"]
	if prio.Type != "multi_select" {
		t.Fatalf("Type = %q, want multi_select", prio.Type)
	}
	for _, o := range prio.Options {
		if o.ID != "" {
			t.Errorf("option %q carries id %q while the type changes", o.Name, o.ID)
		}
	}
}

// Review Focus #5: an option whose key and name both fail to resolve is NEW.
// Giving it another option's id would make it a silent rename, exactly what
// this product exists to make impossible.
func TestUpdateTargetGivesNoIDToAnUnresolvedOption(t *testing.T) {
	desired, applied, actual := fixtureTasks()
	desired.Properties["Prio"] = state.Property{Type: "select", Options: []state.Option{
		{Key: "inconnue", Name: "Inconnue", Color: "gray"},
	}}
	res := CompareDatabase("tasks", &desired, &applied, &actual)

	prio := res.Target.Properties["Prio"]
	if len(prio.Options) != 1 {
		t.Fatalf("options = %d, want 1", len(prio.Options))
	}
	if prio.Options[0].ID != "" {
		t.Errorf("ID = %q, want empty: neither the key nor the name resolves", prio.Options[0].ID)
	}
}

// On an update, a new property goes out with ALL its options, color and group
// included: the plan must show them one by one, as at creation. Hiding them
// would be writing what the plan never showed.
func TestUpdateShowsTheOptionsOfANewProperty(t *testing.T) {
	desired, applied, actual := fixtureTasks()
	desired.Properties["Etat"] = state.Property{Type: "select", Options: []state.Option{
		{Key: "ouvert", Name: "Ouvert", Color: "green"},
		{Key: "clos", Name: "Clos"},
	}}
	res := CompareDatabase("tasks", &desired, &applied, &actual)
	assertOptionLinesMatchTarget(t, res, "Etat", []string{
		`+ option "Ouvert" (property "Etat") color green`,
		`+ option "Clos" (property "Etat") `,
	})
}

// A type change re-creates the options: the YAML's go out new, and the plan
// announces them. The class of the property line stays the measured table's:
// the option lines do not invent another one.
func TestUpdateShowsTheOptionsWrittenOnATypeChange(t *testing.T) {
	desired, applied, actual := fixtureTasks()
	desired.Properties["Prio"] = state.Property{Type: "status", Options: []state.Option{
		{Key: "haute", Name: "Haute", Color: "red", Group: "To-do"},
		{Key: "faite", Name: "Faite", Group: "Complete"},
	}}
	res := CompareDatabase("tasks", &desired, &applied, &actual)
	assertOptionLinesMatchTarget(t, res, "Prio", []string{
		`+ option "Haute" (property "Prio") color red, group To-do`,
		`+ option "Faite" (property "Prio") group Complete`,
		// "Basse" is not redeclared: it disappears with the type change, and its
		// rows with it.
		`- option "Basse" (property "Prio") not redeclared under this name: the type change re-creates the options`,
	})
	for _, d := range res.Changeset.Details {
		if d.Target == `property "Prio"` && d.Class != change.TypeChangeOf("select", "status", []string{"Haute", "Faite"}).Class {
			t.Errorf("class of the type change = %v, want the table's", d.Class)
		}
		if d.Property == "Prio" && d.Op == "+" && d.Class != ClassSafe {
			t.Errorf("line %q: class %v, want safe — it does not invent a class", d.Target, d.Class)
		}
	}
}

// assertOptionLinesMatchTarget checks that the property's option lines are
// exactly the expected ones, in order, that each says what the target writes,
// and that they do not widen the write set: each names a property that already
// carries its own line.
func assertOptionLinesMatchTarget(t *testing.T, res Result, prop string, want []string) {
	t.Helper()
	if res.Target == nil {
		t.Fatalf("Target = nil, Withheld = %q", res.Withheld)
	}
	// Only `+` lines say what the target writes; a `-` line says what the
	// target does NOT write, and so has no option facing it.
	var got, added []string
	propLine := false
	for _, d := range res.Changeset.Details {
		if d.Target == fmt.Sprintf("property %q", prop) ||
			strings.HasPrefix(d.Target, fmt.Sprintf("property %q (", prop)) {
			propLine = true
			continue
		}
		if !strings.HasSuffix(d.Target, fmt.Sprintf("(property %q)", prop)) {
			continue
		}
		if d.Property != prop || d.Field != "" {
			t.Errorf("line %q: Property=%q Field=%q, want Property=%q only",
				d.Target, d.Property, d.Field, prop)
		}
		got = append(got, d.Op+" "+d.Target+" "+d.Note)
		if d.Op == "+" {
			added = append(added, d.Op+" "+d.Target+" "+d.Note)
		}
	}
	if !propLine {
		t.Errorf("no property line for %q: the options would widen the write set", prop)
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("option lines:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	opts := res.Target.Properties[prop].Options
	if len(opts) != len(added) {
		t.Fatalf("the target writes %d options, the plan shows %d", len(opts), len(added))
	}
	for i, o := range opts {
		line := fmt.Sprintf(`+ option %q (property %q) %s`, o.Name, prop, optionAttrNote(o))
		if added[i] != line {
			t.Errorf("option %d written %q, shown %q", i, line, added[i])
		}
		if o.ID != "" {
			t.Errorf("option %q carries id %q: it goes out new", o.Name, o.ID)
		}
	}
}

func TestPlanLinesComparesIcon(t *testing.T) {
	desired, applied, actual := fixtureTasks()
	res := CompareDatabase("tasks", &desired, &applied, &actual)

	var found *resources.Detail
	for i, d := range res.Changeset.Details {
		if d.Field == "icon" {
			found = &res.Changeset.Details[i]
		}
	}
	if found == nil {
		t.Fatal("no icon line while 🔵 → 🟢")
	}
	if found.Op != "~" {
		t.Errorf("Op = %q, want %q", found.Op, "~")
	}
}

// Same non-emptiness guard as for the name: an icon the YAML does not declare
// must produce no line.
func TestPlanLinesIgnoresUndeclaredIcon(t *testing.T) {
	desired, applied, actual := fixtureTasks()
	desired.Icon = ""
	res := CompareDatabase("tasks", &desired, &applied, &actual)
	for _, d := range res.Changeset.Details {
		if d.Field == "icon" {
			t.Errorf("icon line %q while the YAML declares none", d.Target)
		}
	}
}

func TestDriftLinesNamesIconChangedOutside(t *testing.T) {
	_, applied, actual := fixtureTasks()
	actual.Icon = "🟣"
	desired := applied // the YAML sticks to the last applied state
	res := CompareDatabase("tasks", &desired, &applied, &actual)

	joined := strings.Join(res.Drift, "\n")
	if !strings.Contains(joined, "icon") {
		t.Errorf("drift = %q, want a line naming the icon", joined)
	}
}

// D1 (measured on 2026-09-24): an option's color is immutable on the API side
// — the PATCH returns 400 "Cannot update color of select with id/name" and
// fails the whole property. The fix goes through re-creating the option, hence
// ClassMigration, and its cost is measured — the number of rows holding the
// CURRENT option.
func TestOptionColorChangeIsMigrationAndIsMeasured(t *testing.T) {
	desired, applied, actual := fixtureTasks()
	// A single option, a single mismatch: the color.
	desired.Properties["Prio"] = state.Property{Type: "select", Options: []state.Option{
		{Key: "haute", Name: "Haute", Color: "purple"},
		{Key: "basse", Name: "Basse", Color: "blue"},
	}}
	res := CompareDatabase("tasks", &desired, &applied, &actual)

	var found *resources.Detail
	for i, d := range res.Changeset.Details {
		if strings.Contains(d.Note, "color") {
			found = &res.Changeset.Details[i]
		}
	}
	if found == nil {
		t.Fatal("no color line while red → purple")
	}
	if found.Class != change.ClassMigration {
		t.Errorf("Class = %v, want ClassMigration", found.Class)
	}
	if found.Measure == nil {
		t.Fatal("Measure = nil: the fix goes through removing the option, its cost is measured")
	}
	if found.Measure.Option != "Haute" || found.Measure.PropertyType != "select" {
		t.Errorf("Measure = %+v, want Option=Haute PropertyType=select", *found.Measure)
	}
	if found.Count != -1 {
		t.Errorf("Count = %d, want -1 (not measured)", found.Count)
	}
}

// The group is MUTABLE by id (measured on 2026-09-24: "Fait" moved from
// Complete to In progress). It must therefore not follow the color.
func TestOptionGroupChangeStaysSafe(t *testing.T) {
	actual := state.Database{
		ID: "db-1", DataSourceID: "ds-1", Name: "Tasks",
		Properties: map[string]state.Property{
			"Statut": {ID: "s1", Type: "status", Options: []state.Option{
				{ID: "o-fait", Name: "Fait", Color: "green", Group: "Complete"},
			}},
		},
	}
	applied := actual
	applied.Properties = map[string]state.Property{
		"Statut": {ID: "s1", Type: "status", Options: []state.Option{
			{ID: "o-fait", Key: "fait", Name: "Fait", Color: "green", Group: "Complete"},
		}},
	}
	desired := state.Database{Name: "Tasks", Properties: map[string]state.Property{
		"Statut": {Type: "status", Options: []state.Option{
			{Key: "fait", Name: "Fait", Group: "In progress"},
		}},
	}}

	res := CompareDatabase("tasks", &desired, &applied, &actual)
	if len(res.Changeset.Details) != 1 {
		t.Fatalf("details = %d, want 1: %+v", len(res.Changeset.Details), res.Changeset.Details)
	}
	if got := res.Changeset.Details[0].Class; got != change.ClassSafe {
		t.Errorf("Class = %v, want ClassSafe — the group is mutable", got)
	}
}

// A rename carries its cost too: the fix goes through removing the old
// option.
func TestOptionRenameIsMeasured(t *testing.T) {
	desired, applied, actual := fixtureTasks()
	desired.Properties["Prio"] = state.Property{Type: "select", Options: []state.Option{
		{Key: "haute", Name: "Très haute", Color: "red"},
		{Key: "basse", Name: "Basse", Color: "blue"},
	}}
	res := CompareDatabase("tasks", &desired, &applied, &actual)

	for _, d := range res.Changeset.Details {
		if d.Class != change.ClassMigration {
			continue
		}
		if d.Measure == nil {
			t.Fatalf("migration line %q without Measure", d.Target)
		}
		if d.Measure.Option != "Haute" {
			t.Errorf("Measure.Option = %q, want %q (the CURRENT name, the one that filters)",
				d.Measure.Option, "Haute")
		}
		return
	}
	t.Fatal("no migration line while the name changes")
}

func TestUpdateDetailsNameExactlyOnePropertyOrField(t *testing.T) {
	desired, applied, actual := fixtureTasks()
	res := CompareDatabase("tasks", &desired, &applied, &actual)
	if res.Changeset.Kind != resources.KindUpdate {
		t.Fatalf("Kind = %v, want KindUpdate", res.Changeset.Kind)
	}
	if len(res.Changeset.Details) == 0 {
		t.Fatal("no detail: the fixture tests nothing")
	}
	for _, d := range res.Changeset.Details {
		hasProp, hasField := d.Property != "", d.Field != ""
		if hasProp == hasField {
			t.Errorf("detail %q %q: Property=%q Field=%q — exactly one is required",
				d.Op, d.Target, d.Property, d.Field)
		}
	}
}

func TestCreateDetailsNameExactlyOnePropertyOrField(t *testing.T) {
	desired, _, _ := fixtureTasks()
	res := CompareDatabase("tasks", &desired, nil, nil)
	if res.Changeset.Kind != resources.KindCreate {
		t.Fatalf("Kind = %v, want KindCreate", res.Changeset.Kind)
	}
	for _, d := range res.Changeset.Details {
		hasProp, hasField := d.Property != "", d.Field != ""
		if hasProp == hasField {
			t.Errorf("detail %q %q: Property=%q Field=%q — exactly one is required",
				d.Op, d.Target, d.Property, d.Field)
		}
	}
}

func TestCompareDatabase(t *testing.T) {
	cases := []struct {
		name      string
		desired   *state.Database
		applied   *state.Database
		actual    *state.Database
		wantKind  resources.ChangeKind
		wantClass change.Class
		wantLine  string // substring expected in a change line
		wantDrift string // substring expected in the drift ("" = none)
		wantUnman string // substring expected in unmanaged ("" = none)
	}{
		{
			name:      "creation when nothing exists",
			desired:   db("Name", state.Property{Type: "title"}),
			wantKind:  resources.KindCreate,
			wantClass: change.ClassSafe,
			wantLine:  `property "Name"`,
		},
		{
			name:      "no change when all three ways match",
			desired:   db("Statut", status(state.Option{Key: "todo", Name: "À faire", Group: "To-do"})),
			applied:   db("Statut", status(state.Option{ID: "o1", Key: "todo", Name: "À faire", Group: "To-do"})),
			actual:    db("Statut", status(state.Option{ID: "o1", Name: "À faire", Group: "To-do"})),
			wantKind:  resources.KindNone,
			wantClass: change.ClassSafe,
		},
		{
			name:      "option renamed by key",
			desired:   db("Statut", status(state.Option{Key: "done", Name: "Terminé", Group: "Complete"})),
			applied:   db("Statut", status(state.Option{ID: "o1", Key: "done", Name: "Fait", Group: "Complete"})),
			actual:    db("Statut", status(state.Option{ID: "o1", Name: "Fait", Group: "Complete"})),
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassMigration,
			wantLine:  `"Fait" → "Terminé"`,
		},
		{
			// The removal goes through ClassifyOptionRemoval(have.Type, -1) at
			// this point of the plan: the number of affected rows is not measured
			// yet (a later task will wire it in), so -1 says "not measured" and
			// the returned class is ClassUnknownImpact, whatever the type.
			name:      "option renamed without a key: removal plus addition, impact not measured",
			desired:   db("Statut", status(state.Option{Name: "Terminé", Group: "Complete"})),
			applied:   db("Statut", status(state.Option{ID: "o1", Name: "Fait", Group: "Complete"})),
			actual:    db("Statut", status(state.Option{ID: "o1", Name: "Fait", Group: "Complete"})),
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassUnknownImpact,
			wantLine:  `"Fait"`,
		},
		{
			name:      "status option removed: impact not measured at this point of the plan",
			desired:   db("Statut", status(state.Option{Key: "todo", Name: "À faire", Group: "To-do"})),
			applied:   db("Statut", status(state.Option{ID: "o1", Key: "todo", Name: "À faire", Group: "To-do"}, state.Option{ID: "o2", Key: "ko", Name: "Annulé", Group: "Complete"})),
			actual:    db("Statut", status(state.Option{ID: "o1", Name: "À faire", Group: "To-do"}, state.Option{ID: "o2", Name: "Annulé", Group: "Complete"})),
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassUnknownImpact,
			wantLine:  `"Annulé"`,
		},
		{
			name:      "select option removed: impact not measured at this point of the plan",
			desired:   db("Tag", sel(state.Option{Key: "a", Name: "A"})),
			applied:   db("Tag", sel(state.Option{ID: "o1", Key: "a", Name: "A"}, state.Option{ID: "o2", Key: "b", Name: "B"})),
			actual:    db("Tag", sel(state.Option{ID: "o1", Name: "A"}, state.Option{ID: "o2", Name: "B"})),
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassUnknownImpact,
			wantLine:  `"B"`,
		},
		{
			name:      "multi_select option removed: impact not measured at this point of the plan",
			desired:   db("Tags", multiSel(state.Option{Key: "a", Name: "A"})),
			applied:   db("Tags", multiSel(state.Option{ID: "o1", Key: "a", Name: "A"}, state.Option{ID: "o2", Key: "b", Name: "B"})),
			actual:    db("Tags", multiSel(state.Option{ID: "o1", Name: "A"}, state.Option{ID: "o2", Name: "B"})),
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassUnknownImpact,
			wantLine:  `"B"`,
		},
		{
			name:      "option added: safe",
			desired:   db("Tag", sel(state.Option{Key: "a", Name: "A"}, state.Option{Key: "b", Name: "B"})),
			applied:   db("Tag", sel(state.Option{ID: "o1", Key: "a", Name: "A"})),
			actual:    db("Tag", sel(state.Option{ID: "o1", Name: "A"})),
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassSafe,
			wantLine:  `"B"`,
		},
		{
			// The id carried by `applied` ("o1") no longer exists in the actual
			// state: the option was destroyed then re-created outside notion-seed,
			// under a new id but the same name. The key can no longer serve as a
			// bridge — it must fall back on pairing by name rather than produce a
			// phantom removal followed by a phantom creation.
			name:      "declared key with no match in the actual state: falls back on the name",
			desired:   db("Tag", sel(state.Option{Key: "x", Name: "Foo"})),
			applied:   db("Tag", sel(state.Option{ID: "o1", Key: "x", Name: "Foo"})),
			actual:    db("Tag", sel(state.Option{ID: "o2", Name: "Foo"})),
			wantKind:  resources.KindNone,
			wantClass: change.ClassSafe,
			// The id "o1" `applied` carried really vanished from the actual state:
			// it is real drift (an observation), distinct from the plan
			// (Changeset), which must neither re-create nor remove "Foo" — that
			// is what wantKind/wantClass above check.
			wantDrift: "outside notion-seed",
		},
		{
			// The class now comes from the MEASURED table, no longer from refusal
			// on principle: number → rich_text was tried on 2026-09-24 and loses
			// nothing (7 becomes "7"). Announcing it destructive would be
			// precisely the unverified claim this product exists to remove.
			name:      "property type changed: classified by the measured table",
			desired:   db("Estimate", state.Property{Type: "rich_text"}),
			applied:   db("Estimate", state.Property{ID: "p1", Type: "number", Format: "number"}),
			actual:    db("Estimate", state.Property{ID: "p1", Type: "number", Format: "number"}),
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassSafe,
			wantLine:  "number → rich_text",
		},
		{
			// Measured on 2026-09-25: nothing survives number → people.
			name:      "property type changed, measured destructive",
			desired:   db("Estimate", state.Property{Type: "people"}),
			applied:   db("Estimate", state.Property{ID: "p1", Type: "number", Format: "number"}),
			actual:    db("Estimate", state.Property{ID: "p1", Type: "number", Format: "number"}),
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassDestructive,
			wantLine:  "number → people",
		},
		{
			name:      "number format changed: safe",
			desired:   db("Estimate", state.Property{Type: "number", Format: "euro"}),
			applied:   db("Estimate", state.Property{ID: "p1", Type: "number", Format: "number"}),
			actual:    db("Estimate", state.Property{ID: "p1", Type: "number", Format: "number"}),
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassSafe,
			wantLine:  "euro",
		},
		{
			// I1: color and group are stored in the state and compared nowhere
			// before this fix — checked by swapping both groups and changing both
			// colors in a test YAML, which then came out as "No changes".
			//
			// D1 (measured on 2026-09-24): an option's color is IMMUTABLE on the
			// API side — the PATCH returns 400 and fails the whole property. The
			// fix goes through re-creating the option, hence ClassMigration in
			// place of the original ClassSafe.
			name: "option color declared and different: migration required",
			desired: db("Statut", status(
				state.Option{Key: "todo", Name: "À faire", Color: "red", Group: "To-do"})),
			applied: db("Statut", status(
				state.Option{ID: "o1", Key: "todo", Name: "À faire", Color: "blue", Group: "To-do"})),
			actual: db("Statut", status(
				state.Option{ID: "o1", Name: "À faire", Color: "blue", Group: "To-do"})),
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassMigration,
			wantLine:  "color blue → red",
		},
		{
			// Mirror of the previous case: without `color` in the YAML, a color
			// that differs in the actual state is not notion-seed's business —
			// exactly as an omitted description never overwrites Notion's.
			name: "option color absent from the YAML: no line",
			desired: db("Statut", status(
				state.Option{Key: "todo", Name: "À faire", Group: "To-do"})),
			applied: db("Statut", status(
				state.Option{ID: "o1", Key: "todo", Name: "À faire", Color: "blue", Group: "To-do"})),
			actual: db("Statut", status(
				state.Option{ID: "o1", Name: "À faire", Color: "blue", Group: "To-do"})),
			wantKind:  resources.KindNone,
			wantClass: change.ClassSafe,
		},
		{
			// The group of an option moved by hand in Notion must come out both
			// as drift (observation) and in the plan (reconciliation towards the
			// YAML) — same pattern as the option rename tested above.
			name: "drift: group of an option changed in Notion",
			desired: db("Statut", status(
				state.Option{Key: "todo", Name: "À faire", Group: "To-do"})),
			applied: db("Statut", status(
				state.Option{ID: "o1", Key: "todo", Name: "À faire", Group: "To-do"})),
			actual: db("Statut", status(
				state.Option{ID: "o1", Name: "À faire", Group: "In progress"})),
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassSafe,
			wantLine:  "group In progress → To-do",
			wantDrift: "group To-do → In progress",
		},
		{
			// The description is not guarded like the name: a database without
			// `description` in the YAML must never offer to overwrite the one
			// Notion holds — otherwise every plan would show a phantom change.
			name:    "description omitted from the YAML: no line",
			desired: db("Name", state.Property{Type: "title"}),
			applied: db("Name", state.Property{ID: "p1", Type: "title"}),
			actual: &state.Database{Name: "Tasks", Description: "Description existante", Properties: map[string]state.Property{
				"Name": {ID: "p1", Type: "title"},
			}},
			wantKind:  resources.KindNone,
			wantClass: change.ClassSafe,
		},
		{
			name: "description declared and different: both values",
			desired: &state.Database{Name: "Tasks", Description: "Nouvelle description", Properties: map[string]state.Property{
				"Name": {Type: "title"},
			}},
			applied: db("Name", state.Property{ID: "p1", Type: "title"}),
			actual: &state.Database{Name: "Tasks", Description: "Ancienne description", Properties: map[string]state.Property{
				"Name": {ID: "p1", Type: "title"},
			}},
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassSafe,
			wantLine:  `"Ancienne description" → "Nouvelle description"`,
		},
		{
			name:    "unmanaged property: listed, left untouched",
			desired: db("Name", state.Property{Type: "title"}),
			applied: db("Name", state.Property{ID: "p1", Type: "title"}),
			actual: &state.Database{Name: "Tasks", Properties: map[string]state.Property{
				"Name":    {ID: "p1", Type: "title"},
				"Créé le": {ID: "p9", Type: "created_time"},
			}},
			wantKind:  resources.KindNone,
			wantClass: change.ClassSafe,
			// "Créé le" is in no known `applied`: it is also drift (appeared
			// outside notion-seed), on top of being unmanaged.
			wantDrift: `"Créé le"`,
			wantUnman: `"Créé le"`,
		},
		{
			name:    "drift: option renamed in Notion",
			desired: db("Statut", status(state.Option{Key: "done", Name: "Fait", Group: "Complete"})),
			applied: db("Statut", status(state.Option{ID: "o1", Key: "done", Name: "Fait", Group: "Complete"})),
			actual:  db("Statut", status(state.Option{ID: "o1", Name: "Terminé", Group: "Complete"})),
			// The YAML wins: the return to "Fait" is planned again. The assertion
			// targets the rename text, not only "Fait": a REMOVAL line would also
			// contain "Fait" and would let through a regression that confused
			// rename and deletion.
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassMigration,
			wantDrift: `renamed to "Terminé"`,
		},
		{
			name:      "orphan database: destruction planned",
			applied:   db("Name", state.Property{ID: "p1", Type: "title"}),
			actual:    db("Name", state.Property{ID: "p1", Type: "title"}),
			wantKind:  resources.KindDestroy,
			wantClass: change.ClassDestructive,
			wantLine:  "database.tasks",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CompareDatabase("tasks", tc.desired, tc.applied, tc.actual)

			if got.Changeset.Kind != tc.wantKind {
				t.Errorf("Kind = %v, want %v", got.Changeset.Kind, tc.wantKind)
			}
			if c := WorstClass(got.Changeset.Details); c != tc.wantClass {
				t.Errorf("class = %v, want %v (details: %+v)", c, tc.wantClass, got.Changeset.Details)
			}
			if tc.wantLine != "" && !containsSub(detailStrings(got.Changeset.Details), tc.wantLine) {
				t.Errorf("no line contains %q: %+v", tc.wantLine, got.Changeset.Details)
			}
			if tc.wantDrift == "" && len(got.Drift) != 0 {
				t.Errorf("unexpected drift: %v", got.Drift)
			}
			if tc.wantDrift != "" && !containsSub(got.Drift, tc.wantDrift) {
				t.Errorf("expected drift containing %q, got %v", tc.wantDrift, got.Drift)
			}
			if tc.wantUnman == "" && len(got.Unmanaged) != 0 {
				t.Errorf("unexpected unmanaged: %v", got.Unmanaged)
			}
			if tc.wantUnman != "" && !containsSub(got.Unmanaged, tc.wantUnman) {
				t.Errorf("expected unmanaged containing %q, got %v", tc.wantUnman, got.Unmanaged)
			}
		})
	}
}

func TestCompareDatabaseSaysNothingWhenRemoteWasNotRead(t *testing.T) {
	// --skip-preflight reads nothing. An already imported database must above
	// all not come out as a creation.
	desired := db("Name", state.Property{Type: "title"})
	applied := db("Name", state.Property{ID: "p1", Type: "title"})

	got := CompareDatabase("tasks", desired, applied, nil)

	if got.Changeset.Kind != resources.KindNone {
		t.Errorf("Kind = %v, want KindNone", got.Changeset.Kind)
	}
	if len(got.Changeset.Details) != 0 {
		t.Errorf("no detail expected without reading the actual state: %+v", got.Changeset.Details)
	}
}

// Review Focus 4: a hand-written state can hold a database without
// `properties`. The nil map must behave as empty.
func TestCompareDatabaseHandlesNilProperties(t *testing.T) {
	desired := &state.Database{Name: "Tasks"}
	applied := &state.Database{ID: "db1", Name: "Tasks"}
	actual := &state.Database{ID: "db1", Name: "Tasks"}

	got := CompareDatabase("tasks", desired, applied, actual)

	if got.Changeset.Kind != resources.KindNone {
		t.Errorf("Kind = %v, want KindNone", got.Changeset.Kind)
	}
}

func TestCompareDatabaseRenamedPropertyIsCreationPlusUnmanaged(t *testing.T) {
	desired := db("Charge", state.Property{Type: "number"})
	applied := db("Estimate", state.Property{ID: "p1", Type: "number"})
	actual := db("Estimate", state.Property{ID: "p1", Type: "number"})

	got := CompareDatabase("tasks", desired, applied, actual)

	lines := detailStrings(got.Changeset.Details)
	if !containsSub(lines, `"Charge"`) {
		t.Errorf("the new property must be created: %v", lines)
	}
	if !containsSub(lines, "Estimate") {
		t.Errorf("the line must warn that Estimate stays in place: %v", lines)
	}
	if !containsSub(got.Unmanaged, `"Estimate"`) {
		t.Errorf("Estimate must appear as unmanaged: %v", got.Unmanaged)
	}
	if WorstClass(got.Changeset.Details) != change.ClassSafe {
		t.Error("a property rename destroys nothing, it duplicates")
	}
}

// Fix 1 (review round 1): `claimed` was indexed by name, so an option renamed
// by key freed its old name, and the fallback-by-name branch thought that name
// still taken — a declared option dropped out of the plan without a line.
// Triple provided by the review.
func TestCompareDatabaseOptionRenameFreesNameForNewOption(t *testing.T) {
	desired := db("Statut", status(
		state.Option{Key: "a", Name: "Terminé"},
		state.Option{Name: "Fait"},
	))
	applied := db("Statut", status(state.Option{ID: "o1", Key: "a", Name: "Fait"}))
	actual := db("Statut", status(state.Option{ID: "o1", Name: "Fait"}))

	got := CompareDatabase("tasks", desired, applied, actual)

	if got.Changeset.Kind != resources.KindUpdate {
		t.Errorf("Kind = %v, want KindUpdate", got.Changeset.Kind)
	}
	if c := WorstClass(got.Changeset.Details); c != change.ClassMigration {
		t.Errorf("class = %v, want ClassMigration (details: %+v)", c, got.Changeset.Details)
	}

	lines := detailStrings(got.Changeset.Details)
	if !containsSub(lines, `"Fait" → "Terminé"`) {
		t.Errorf("the migration by key must appear: %v", lines)
	}
	if !containsSub(lines, `+ option "Fait"`) {
		t.Errorf(
			"the creation of \"Fait\" must appear: without it the YAML declares "+
				"an option that will never be created, without the plan showing it: %v",
			lines)
	}
}

// Blind spot of the table (review round 1): a rename by key and a removal on
// the same status property must produce BOTH lines, and the returned class
// must stay the more severe of the two. The removal is not measured at this
// point of the plan (ClassifyOptionRemoval receives -1 there), so it is the
// unknown impact that dominates the migration — not the silent rewrite the
// measurement will reveal once wired in.
func TestCompareDatabaseStatusRenameAndRemovalTogether(t *testing.T) {
	desired := db("Statut", status(state.Option{Key: "a", Name: "Migré"}))
	applied := db("Statut", status(
		state.Option{ID: "o1", Key: "a", Name: "Ancien"},
		state.Option{ID: "o2", Key: "b", Name: "Obsolète"},
	))
	actual := db("Statut", status(
		state.Option{ID: "o1", Name: "Ancien"},
		state.Option{ID: "o2", Name: "Obsolète"},
	))

	got := CompareDatabase("tasks", desired, applied, actual)

	if got.Changeset.Kind != resources.KindUpdate {
		t.Errorf("Kind = %v, want KindUpdate", got.Changeset.Kind)
	}
	if c := WorstClass(got.Changeset.Details); c != change.ClassUnknownImpact {
		t.Errorf("class = %v, want ClassUnknownImpact (details: %+v)", c, got.Changeset.Details)
	}

	lines := detailStrings(got.Changeset.Details)
	if !containsSub(lines, `"Ancien" → "Migré"`) {
		t.Errorf("the migration must appear: %v", lines)
	}
	if !containsSub(lines, `"Obsolète"`) {
		t.Errorf("the removal must appear: %v", lines)
	}
}

// An option removal must carry the measurement REQUEST, not the measurement:
// the comparator stays pure, that is where the product's safety lives.
func TestCompareDatabaseRequestsAMeasurementForOptionRemoval(t *testing.T) {
	desired := state.Database{
		Name: "Tasks",
		Properties: map[string]state.Property{
			"Statut": {Type: "status", Options: []state.Option{{Key: "todo", Name: "À faire"}}},
		},
	}
	applied := state.Database{
		ID: "db-1", Name: "Tasks",
		Properties: map[string]state.Property{
			"Statut": {ID: "p1", Type: "status", Options: []state.Option{
				{ID: "o1", Key: "todo", Name: "À faire"},
				{ID: "o2", Name: "Annulé"},
			}},
		},
	}
	res := CompareDatabase("tasks", &desired, &applied, &applied)

	var found *resources.Detail
	for i := range res.Changeset.Details {
		if res.Changeset.Details[i].Op == "-" {
			found = &res.Changeset.Details[i]
		}
	}
	if found == nil {
		t.Fatal("no option removal line")
	}
	if found.Measure == nil {
		t.Fatal("the removal line carries no measurement request")
	}
	if found.Measure.Property != "Statut" || found.Measure.Option != "Annulé" ||
		found.Measure.PropertyType != "status" {
		t.Errorf("Measure = %+v", *found.Measure)
	}
	if found.Count != -1 {
		t.Errorf("Count = %d, want -1 as long as nothing was measured", found.Count)
	}
	if found.Class != change.ClassUnknownImpact {
		t.Errorf("Class = %v, want ClassUnknownImpact before measurement", found.Class)
	}
}

// A type change carries its request too — the number of non-empty values of
// the column — and its class comes from the measured table.
func TestCompareDatabaseClassifiesTypeChangeFromTheMeasuredTable(t *testing.T) {
	desired := state.Database{
		Name:       "Clients",
		Properties: map[string]state.Property{"Tags": {Type: "select"}},
	}
	applied := state.Database{
		ID: "db-1", Name: "Clients",
		Properties: map[string]state.Property{"Tags": {ID: "p1", Type: "multi_select"}},
	}
	res := CompareDatabase("clients", &desired, &applied, &applied)

	var found *resources.Detail
	for i := range res.Changeset.Details {
		if strings.Contains(res.Changeset.Details[i].Target, `property "Tags"`) {
			found = &res.Changeset.Details[i]
		}
	}
	if found == nil {
		t.Fatal("no type change line")
	}
	// multi_select → select: measured as a silent rewrite on 2026-09-24.
	if found.Class != change.ClassSilentRewrite {
		t.Errorf("Class = %v, want ClassSilentRewrite", found.Class)
	}
	if found.Measure == nil || found.Measure.Option != "" {
		t.Errorf("Measure = %+v, want a request to count the non-empty values", found.Measure)
	}
}

// An option addition costs nothing: no measurement must be requested, so no
// call will be paid.
func TestCompareDatabaseRequestsNoMeasurementForSafeDetails(t *testing.T) {
	desired := state.Database{
		Name: "Tasks",
		Properties: map[string]state.Property{
			"Statut": {Type: "status", Options: []state.Option{
				{Key: "todo", Name: "À faire"}, {Key: "new", Name: "Neuve"}}},
		},
	}
	applied := state.Database{
		ID: "db-1", Name: "Tasks",
		Properties: map[string]state.Property{
			"Statut": {ID: "p1", Type: "status", Options: []state.Option{
				{ID: "o1", Key: "todo", Name: "À faire"}}},
		},
	}
	res := CompareDatabase("tasks", &desired, &applied, &applied)
	for _, d := range res.Changeset.Details {
		if d.Op == "+" && d.Measure != nil {
			t.Errorf("line %q requests a measurement while it costs nothing", d.Target)
		}
	}
}

// No detail emitted by the comparator must carry Count == 0.
//
// WHY it is a bug and not a whim: 0 reads as "0 rows affected", hence "nothing
// to lose", hence "safe". But CompareDatabase is PURE — it counts nothing, it
// only EMITS measurement requests. A 0 coming out of here would therefore be a
// claim of harmlessness nobody verified, and that is exactly the flaw this
// product exists to make impossible. Before the measurement pass, the only
// honest count is -1: "we don't know".
//
// This test exists because resources.NewDetail is not enough: a
// `resources.Detail{...}` literal is always possible, and the Count field
// defaults to 0 in Go. The constructor is the convenience; this test is the
// safeguard. It sweeps a range of cases, not just one, because the hole can
// open in any branch of planLines, optionLines or createLines.
func TestCompareDatabaseNeverEmitsAnUnmeasuredZeroCount(t *testing.T) {
	withColor := func(name, color, group string) state.Property {
		return state.Property{ID: "p1", Type: "status", Options: []state.Option{
			{ID: "o1", Key: "k", Name: name, Color: color, Group: group},
		}}
	}

	cases := []struct {
		name                     string
		desired, applied, actual *state.Database
	}{
		{
			name:    "full creation",
			desired: db("Statut", status(state.Option{Key: "todo", Name: "À faire", Color: "blue", Group: "To-do"})),
		},
		{
			name: "name and description updated",
			desired: &state.Database{Name: "Nouveau", Description: "Nouvelle", Properties: map[string]state.Property{
				"Name": {Type: "title"},
			}},
			applied: db("Name", state.Property{ID: "p1", Type: "title"}),
			actual: &state.Database{Name: "Ancien", Description: "Ancienne", Properties: map[string]state.Property{
				"Name": {ID: "p1", Type: "title"},
			}},
		},
		{
			name: "property added",
			desired: &state.Database{Name: "Tasks", Properties: map[string]state.Property{
				"Name":     {Type: "title"},
				"Estimate": {Type: "number"},
			}},
			applied: db("Name", state.Property{ID: "p1", Type: "title"}),
			actual:  db("Name", state.Property{ID: "p1", Type: "title"}),
		},
		{
			name:    "type change in the measured table",
			desired: db("Tags", state.Property{Type: "multi_select"}),
			applied: db("Tags", state.Property{ID: "p1", Type: "select"}),
			actual:  db("Tags", state.Property{ID: "p1", Type: "select"}),
		},
		{
			name:    "type change outside the measured table",
			desired: db("Estimate", state.Property{Type: "people"}),
			applied: db("Estimate", state.Property{ID: "p1", Type: "number"}),
			actual:  db("Estimate", state.Property{ID: "p1", Type: "number"}),
		},
		{
			name:    "number format updated",
			desired: db("Estimate", state.Property{Type: "number", Format: "euro"}),
			applied: db("Estimate", state.Property{ID: "p1", Type: "number", Format: "number"}),
			actual:  db("Estimate", state.Property{ID: "p1", Type: "number", Format: "number"}),
		},
		{
			name: "option added",
			desired: db("Statut", status(
				state.Option{Key: "todo", Name: "À faire"},
				state.Option{Name: "Neuve"},
			)),
			applied: db("Statut", status(state.Option{ID: "o1", Key: "todo", Name: "À faire"})),
			actual:  db("Statut", status(state.Option{ID: "o1", Name: "À faire"})),
		},
		{
			name:    "status option removed",
			desired: db("Statut", status(state.Option{Key: "todo", Name: "À faire"})),
			applied: db("Statut", status(
				state.Option{ID: "o1", Key: "todo", Name: "À faire"},
				state.Option{ID: "o2", Name: "Annulé"},
			)),
			actual: db("Statut", status(
				state.Option{ID: "o1", Name: "À faire"},
				state.Option{ID: "o2", Name: "Annulé"},
			)),
		},
		{
			name:    "select option removed",
			desired: db("Tag", sel(state.Option{Key: "a", Name: "A"})),
			applied: db("Tag", sel(
				state.Option{ID: "o1", Key: "a", Name: "A"},
				state.Option{ID: "o2", Name: "B"},
			)),
			actual: db("Tag", sel(
				state.Option{ID: "o1", Name: "A"},
				state.Option{ID: "o2", Name: "B"},
			)),
		},
		{
			name:    "multi_select option removed",
			desired: db("Tag", multiSel(state.Option{Key: "a", Name: "A"})),
			applied: db("Tag", multiSel(
				state.Option{ID: "o1", Key: "a", Name: "A"},
				state.Option{ID: "o2", Name: "B"},
			)),
			actual: db("Tag", multiSel(
				state.Option{ID: "o1", Name: "A"},
				state.Option{ID: "o2", Name: "B"},
			)),
		},
		{
			name:    "option renamed by key",
			desired: db("Statut", status(state.Option{Key: "done", Name: "Terminé"})),
			applied: db("Statut", status(state.Option{ID: "o1", Key: "done", Name: "Fait"})),
			actual:  db("Statut", status(state.Option{ID: "o1", Name: "Fait"})),
		},
		{
			name:    "option color and group updated",
			desired: db("Statut", withColor("Fait", "green", "Complete")),
			applied: db("Statut", withColor("Fait", "blue", "To-do")),
			actual:  db("Statut", withColor("Fait", "blue", "To-do")),
		},
		{
			name:    "destruction of an orphan database",
			applied: db("Name", state.Property{ID: "p1", Type: "title"}),
			actual:  db("Name", state.Property{ID: "p1", Type: "title"}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := CompareDatabase("tasks", tc.desired, tc.applied, tc.actual)
			if len(res.Changeset.Details) == 0 {
				t.Fatal("no detail: this case no longer covers anything, fix the triple")
			}
			for _, d := range res.Changeset.Details {
				if d.Count == 0 {
					t.Errorf(
						"detail %q %q carries Count = 0, hence \"0 rows affected\", "+
							"hence \"safe\" — while nothing was measured; want -1",
						d.Op, d.Target)
				}
			}
		})
	}
}

// A type change the MEASURED table says is safe must cost no call: the count
// of non-empty values would change neither its class nor the user's decision.
// A plan that holds only a `number → rich_text` would otherwise pay for a
// count against the API for a number that changes nothing.
func TestCompareDatabaseRequestsNoMeasurementForASafeTypeChange(t *testing.T) {
	desired := state.Database{
		Name:       "Clients",
		Properties: map[string]state.Property{"Estimate": {Type: "rich_text"}},
	}
	applied := state.Database{
		ID: "db-1", Name: "Clients",
		Properties: map[string]state.Property{
			"Estimate": {ID: "p1", Type: "number", Format: "number"}},
	}
	res := CompareDatabase("clients", &desired, &applied, &applied)

	var found *resources.Detail
	for i := range res.Changeset.Details {
		if strings.Contains(res.Changeset.Details[i].Target, `property "Estimate"`) {
			found = &res.Changeset.Details[i]
		}
	}
	if found == nil {
		t.Fatal("no type change line")
	}
	// number → rich_text: measured safe on 2026-09-24, so nothing to count.
	if found.Class != change.ClassSafe {
		t.Fatalf("Class = %v, want ClassSafe", found.Class)
	}
	if found.Measure != nil {
		t.Errorf("Measure = %+v, want nil: a safe type change costs no call",
			found.Measure)
	}
	// The line stays "not measured": 0 would mean "no rows affected", a claim
	// nobody verified.
	if found.Count != -1 {
		t.Errorf("Count = %d, want -1", found.Count)
	}
}

func detailStrings(ds []resources.Detail) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.Op+" "+d.Target+" "+d.Note)
	}
	return out
}

func containsSub(lines []string, sub string) bool {
	for _, l := range lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

// Measured on 2026-09-25 on select → multi_select: a row keeps its value only
// if an option with the SAME NAME goes in the payload. Those the YAML does not
// redeclare disappear from the schema, and their rows are emptied. The plan
// must therefore announce each one, measured as an ordinary removal —
// otherwise this type change, classified safe by the table, loses values
// without any line or any --fail-on saying so.
func TestTypeChangeAnnouncesEveryOptionItDrops(t *testing.T) {
	actual := state.Database{
		ID: "db-1", DataSourceID: "ds-1", Name: "Tasks",
		Properties: map[string]state.Property{
			"Prio": {ID: "p1", Type: "select", Options: []state.Option{
				{ID: "o-haute", Name: "Haute", Color: "red"},
				{ID: "o-basse", Name: "Basse", Color: "blue"},
				{ID: "o-moy", Name: "Moyenne", Color: "yellow"},
			}},
		},
	}
	applied := state.Database{
		ID: "db-1", DataSourceID: "ds-1", Name: "Tasks",
		Properties: map[string]state.Property{
			"Prio": {ID: "p1", Type: "select", Options: []state.Option{
				{ID: "o-haute", Key: "haute", Name: "Haute", Color: "red"},
				{ID: "o-basse", Key: "basse", Name: "Basse", Color: "blue"},
				{ID: "o-moy", Key: "moyenne", Name: "Moyenne", Color: "yellow"},
			}},
		},
	}
	// "Haute" keeps its name under another key: the name alone counts, since
	// the API re-creates the ids. "Moyenne" keeps its key but changes name:
	// under a type change, it is a loss, not a rename.
	desired := state.Database{Properties: map[string]state.Property{
		"Prio": {Type: "multi_select", Options: []state.Option{
			{Key: "h", Name: "Haute", Color: "red"},
			{Key: "moyenne", Name: "Normale"},
		}},
	}}
	res := CompareDatabase("tasks", &desired, &applied, &actual)

	var removed []resources.Detail
	for _, d := range res.Changeset.Details {
		if d.Op == "-" {
			removed = append(removed, d)
		}
	}
	var names []string
	for _, d := range removed {
		names = append(names, d.Target)
	}
	want := []string{`option "Basse" (property "Prio")`, `option "Moyenne" (property "Prio")`}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("removals = %q, want %q", names, want)
	}
	for _, d := range removed {
		if d.Property != "Prio" || d.Field != "" {
			t.Errorf("%s: Property=%q Field=%q, want Property=\"Prio\" only", d.Target, d.Property, d.Field)
		}
		if d.Note != "not redeclared under this name: the type change re-creates the options" {
			t.Errorf("%s: Note = %q", d.Target, d.Note)
		}
		if d.Class != change.ClassUnknownImpact || d.Count != -1 {
			t.Errorf("%s: {Class:%v Count:%d}, want {unknown impact -1} before measurement",
				d.Target, d.Class, d.Count)
		}
		if d.Measure == nil {
			t.Fatalf("%s: no measurement request", d.Target)
		}
	}
	// The measurement carries the OLD type: it is what filters the rows before
	// the write, and the current name that finds them.
	wantMeasure := resources.Measurement{
		Property: "Prio", PropertyType: "select", Option: "Basse", Retyped: true,
		TargetType: "multi_select",
	}
	if !reflect.DeepEqual(*removed[0].Measure, wantMeasure) {
		t.Errorf("Measure = %+v, want %+v", *removed[0].Measure, wantMeasure)
	}
	// The target stays the one the 2026-09-25 measurement says is right: only
	// the declared options, by name, with no id.
	var sent []string
	for _, o := range res.Target.Properties["Prio"].Options {
		sent = append(sent, o.Name)
		if o.ID != "" {
			t.Errorf("option %q carries id %q: it goes out new", o.Name, o.ID)
		}
	}
	if !reflect.DeepEqual(sent, []string{"Haute", "Normale"}) {
		t.Errorf("options written = %q", sent)
	}
}

// A change to a type WITHOUT options (select → rich_text) has no name to find:
// what it does to the values is the business of the table of pairs and of the
// count of non-empty values, not of a removal line per option.
func TestTypeChangeToATypeWithoutOptionsAnnouncesNoRemoval(t *testing.T) {
	actual := state.Database{
		ID: "db-1", DataSourceID: "ds-1", Name: "Tasks",
		Properties: map[string]state.Property{
			"Prio": sel(state.Option{ID: "o1", Name: "Haute"}),
		},
	}
	desired := state.Database{Properties: map[string]state.Property{
		"Prio": {Type: "rich_text"},
	}}
	res := CompareDatabase("tasks", &desired, &actual, &actual)
	for _, d := range res.Changeset.Details {
		if d.Op == "-" {
			t.Errorf("unexpected removal line: %s", d.Target)
		}
	}
}

// A new option on an existing property goes out with its color and group, as
// at creation: the plan must show them, otherwise apply writes what it never
// displayed.
func TestUpdateShowsTheAttributesOfANewOption(t *testing.T) {
	desired, applied, actual := fixtureTasks()
	desired.Properties["Etat"] = state.Property{Type: "status", Options: []state.Option{
		{Key: "todo", Name: "À faire", Group: "To-do"},
		{Key: "bloque", Name: "Bloqué", Color: "red", Group: "In progress"},
	}}
	actual.Properties["Etat"] = state.Property{ID: "e1", Type: "status", Options: []state.Option{
		{ID: "o-todo", Name: "À faire", Group: "To-do"},
	}}
	applied.Properties["Etat"] = actual.Properties["Etat"]
	res := CompareDatabase("tasks", &desired, &applied, &actual)

	lines := detailStrings(res.Changeset.Details)
	for _, want := range []string{
		`+ option "Moyenne" (property "Prio") color orange`,
		`+ option "Bloqué" (property "Etat") color red, group In progress`,
	} {
		if !containsSub(lines, want) {
			t.Errorf("missing %q in:\n%s", want, strings.Join(lines, "\n"))
		}
	}
	// And it is indeed what the target writes.
	var sent state.Option
	for _, o := range res.Target.Properties["Prio"].Options {
		if o.Name == "Moyenne" {
			sent = o
		}
	}
	if sent.Color != "orange" {
		t.Errorf("option written = %+v, want color orange", sent)
	}
}

// The write permission is Withheld == "" for all three kinds of change. A
// destruction gets it WITHOUT a target: there is no state afterwards.
func TestCompareDatabaseAuthorizesADestroyWithoutATarget(t *testing.T) {
	applied := state.Database{ID: "db-1", DataSourceID: "ds-1", Name: "Tasks"}
	actual := applied
	res := CompareDatabase("tasks", nil, &applied, &actual)
	if res.Changeset.Kind != resources.KindDestroy {
		t.Fatalf("Kind = %v, want KindDestroy", res.Changeset.Kind)
	}
	if res.Withheld != "" {
		t.Errorf("Withheld = %q, want empty: a destruction is allowed", res.Withheld)
	}
	if res.Target != nil {
		t.Errorf("Target = %+v, want nil: a destruction has no state afterwards", res.Target)
	}
}

// A destruction requests the count of ALL rows: it is what goes to the trash
// with the database. Not measured, it stays at -1, never at 0.
func TestCompareDatabaseAsksToCountTheRowsOfADestroy(t *testing.T) {
	applied := state.Database{ID: "db-1", DataSourceID: "ds-1", Name: "Tasks"}
	actual := applied
	res := CompareDatabase("tasks", nil, &applied, &actual)
	if len(res.Changeset.Details) != 1 {
		t.Fatalf("Details = %+v, want one line", res.Changeset.Details)
	}
	d := res.Changeset.Details[0]
	if d.Measure == nil || !d.Measure.AllRows || d.Count != -1 || d.Class != ClassDestructive {
		t.Errorf("Detail = %+v, want an AllRows request, Count -1, destructive", d)
	}
}

// The API refuses to change the type of a title property, both ways (400,
// measured on 2026-09-25). Sending it would fail the whole PATCH: the
// resource is withheld, with its own procedure — not the option one.
func TestTitleTypeChangeWithholdsTheResource(t *testing.T) {
	for _, tc := range []struct{ from, to string }{
		{"title", "rich_text"},
		{"rich_text", "title"},
	} {
		actual := state.Database{
			ID: "db-1", DataSourceID: "ds-1", Name: "Tasks",
			Properties: map[string]state.Property{"Nom": {ID: "p1", Type: tc.from}},
		}
		desired := state.Database{Properties: map[string]state.Property{
			"Nom": {Type: tc.to, Options: nil},
		}}
		res := CompareDatabase("tasks", &desired, &actual, &actual)

		if res.Target != nil {
			t.Errorf("%s → %s: Target non-nil: apply would send a PATCH the API refuses", tc.from, tc.to)
		}
		if len(res.Changeset.Details) != 1 {
			t.Fatalf("%s → %s: details = %+v, want the one refused line", tc.from, tc.to, res.Changeset.Details)
		}
		d := res.Changeset.Details[0]
		if d.Class != change.ClassMigration || d.Measure != nil {
			t.Errorf("%s → %s: {Class:%v Measure:%+v}, want migration required, nothing to count",
				tc.from, tc.to, d.Class, d.Measure)
		}
		if !strings.Contains(d.Note, "400") {
			t.Errorf("%s → %s: Note = %q, want the API refusal named", tc.from, tc.to, d.Note)
		}
		if !strings.Contains(res.Withheld, "title") || !strings.Contains(res.Withheld, "  → ") {
			t.Errorf("%s → %s: Withheld = %q, want the title procedure", tc.from, tc.to, res.Withheld)
		}
		if strings.Contains(res.Withheld, "option") {
			t.Errorf("%s → %s: Withheld = %q speaks of options", tc.from, tc.to, res.Withheld)
		}
	}
}

// The line says what survives, measured: the user reads it to decide.
func TestTypeChangeLineSaysWhatSurvives(t *testing.T) {
	actual := state.Database{
		ID: "db-1", DataSourceID: "ds-1", Name: "Tasks",
		Properties: map[string]state.Property{"Notes": {ID: "p1", Type: "rich_text"}},
	}
	desired := state.Database{Properties: map[string]state.Property{
		"Notes": {Type: "select", Options: []state.Option{{Name: "Un"}}},
	}}
	res := CompareDatabase("tasks", &desired, &actual, &actual)
	lines := detailStrings(res.Changeset.Details)
	if !containsSub(lines, "rich_text → select: the API creates no option: values survive only where an option with the same text is declared") {
		t.Errorf("lines:\n%s", strings.Join(lines, "\n"))
	}
	// A declared option brings the cut at the first comma: a rewrite.
	if res.Changeset.Details[0].Class != change.ClassSilentRewrite {
		t.Errorf("Class = %v, want silent rewrite", res.Changeset.Details[0].Class)
	}
}

// The property line carries the count the measured table prescribes: the
// filter, its bound, the declared options whose rows survive, and why the
// figure is only a bound.
func TestTypeChangeCarriesTheMeasuredCount(t *testing.T) {
	actual := state.Database{
		ID: "db-1", DataSourceID: "ds-1", Name: "Tasks",
		Properties: map[string]state.Property{"Notes": {ID: "p1", Type: "rich_text"}},
	}
	desired := state.Database{Properties: map[string]state.Property{
		"Notes": {Type: "select", Options: []state.Option{{Name: "Un"}, {Name: "Deux"}}},
	}}
	res := CompareDatabase("tasks", &desired, &actual, &actual)
	m := res.Changeset.Details[0].Measure
	if m == nil {
		t.Fatal("no measurement request on the type change")
	}
	want := change.TypeChangeOf("rich_text", "select", []string{"Un", "Deux"})
	if m.PropertyType != "rich_text" || m.TargetType != "select" ||
		m.Count != want.Count || m.Bound != change.BoundAtLeast ||
		!reflect.DeepEqual(m.Except, []string{"Un", "Deux"}) || m.Caveat == "" {
		t.Errorf("Measure = %+v", *m)
	}
}

// Toward status, the removal lines of a type change carry the new type: it
// decides the fate of the rows.
func TestRetypedRemovalTowardStatusCarriesTheNewType(t *testing.T) {
	actual := state.Database{
		ID: "db-1", DataSourceID: "ds-1", Name: "Tasks",
		Properties: map[string]state.Property{
			"Prio": sel(state.Option{ID: "o1", Name: "Haute"}, state.Option{ID: "o2", Name: "Basse"}),
		},
	}
	desired := state.Database{Properties: map[string]state.Property{
		"Prio": {Type: "status", Options: []state.Option{{Name: "Haute", Group: "To-do"}}},
	}}
	res := CompareDatabase("tasks", &desired, &actual, &actual)
	for _, d := range res.Changeset.Details {
		if d.Op == "-" && (d.Measure == nil || d.Measure.TargetType != "status") {
			t.Errorf("%s: Measure = %+v, want TargetType status", d.Target, d.Measure)
		}
	}
}

// multi_select → select: the removal lines count the rows holding "Basse";
// the property line must not count them again in another family.
func TestMultiSelectTypeChangeDoesNotCountRemovedRowsTwice(t *testing.T) {
	actual := state.Database{
		ID: "db-1", DataSourceID: "ds-1", Name: "Tasks",
		Properties: map[string]state.Property{
			"Tags": multiSel(state.Option{ID: "o1", Name: "Haute"}, state.Option{ID: "o2", Name: "Basse"}),
		},
	}
	desired := state.Database{Properties: map[string]state.Property{
		"Tags": {Type: "select", Options: []state.Option{{Name: "Haute"}}},
	}}
	res := CompareDatabase("tasks", &desired, &actual, &actual)
	m := res.Changeset.Details[0].Measure
	if m == nil || !reflect.DeepEqual(m.Except, []string{"Basse"}) {
		t.Errorf("property line Measure = %+v, want the rows holding Basse excluded", m)
	}
}

// multi_select → select keeps only the FIRST value (measured on 2026-09-24).
// A row [B, A], B not redeclared, is counted on B's removal line only — and
// loses A as well, which no line counts. The removal lines therefore bound the
// loss from below, and the total must say "at least".
func TestRetypedMultiSelectRemovalIsALowerBound(t *testing.T) {
	actual := state.Database{
		ID: "db-1", DataSourceID: "ds-1", Name: "Tasks",
		Properties: map[string]state.Property{
			"Tags": multiSel(state.Option{ID: "o1", Name: "B"}, state.Option{ID: "o2", Name: "A"}),
		},
	}
	desired := state.Database{Properties: map[string]state.Property{
		"Tags": {Type: "select", Options: []state.Option{{Name: "A"}}},
	}}
	res := CompareDatabase("tasks", &desired, &actual, &actual)

	details := res.Changeset.Details
	for i := range details {
		d := &details[i]
		switch {
		case d.Op == "-":
			if d.Measure.Bound != change.BoundAtLeast {
				t.Errorf("%s: Bound = %v, want at least", d.Target, d.Measure.Bound)
			}
			// The row [B, A]: one row holds B.
			d.Count, d.Class = 1, change.ClassDestructive
		case d.Measure != nil:
			// The property line excludes rows holding B: none left.
			d.Count, d.Class = 0, change.ClassSafe
		}
	}
	p := &Plan{Changes: []Change{{Resource: "database.tasks", Kind: resources.KindUpdate, Details: details}}}
	if got := Impact(p); got != "Impact: at least 1 values lost." {
		t.Errorf("Impact = %q, want a lower bound", got)
	}
}
