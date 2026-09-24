// SPDX-License-Identifier: GPL-3.0-or-later

package apply

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/tykok/notion-seed/core/diff"
	"github.com/tykok/notion-seed/core/providers/notion/resources"
	"github.com/tykok/notion-seed/core/providers/notion/transport"
	"github.com/tykok/notion-seed/core/state"
)

const testParentPageID = "33333333-3333-4333-8333-333333333333"

type creatorFunc func(ctx context.Context, body []byte) (resources.CreatedDatabase, error)

func (f creatorFunc) Create(ctx context.Context, body []byte) (resources.CreatedDatabase, error) {
	return f(ctx, body)
}

// refuseToCreate sert aux tests où aucune création ne doit être tentée.
func refuseToCreate(t *testing.T) creatorFunc {
	t.Helper()
	return func(context.Context, []byte) (resources.CreatedDatabase, error) {
		t.Error("aucune création ne devait être tentée")
		return resources.CreatedDatabase{}, errors.New("création interdite dans ce test")
	}
}

func targetDatabase(name string) *state.Database {
	return &state.Database{
		Name:       name,
		Properties: map[string]state.Property{"Name": {Type: "title"}},
	}
}

func createdFrom(id, name string) resources.CreatedDatabase {
	return resources.CreatedDatabase{
		ID:           id,
		DataSourceID: "ds-" + id,
		Remote: resources.RemoteDatabase{
			ID:           id,
			DataSourceID: "ds-" + id,
			Name:         name,
			Found:        true,
			Properties: map[string]resources.RemoteProperty{
				"Name": {ID: "title", Type: "title"},
			},
		},
	}
}

func createChange(key, name string) diff.Change {
	return diff.Change{
		Resource: "database." + key,
		Key:      key,
		Kind:     resources.KindCreate,
		Target:   targetDatabase(name),
	}
}

func emptySnapshot() *state.Snapshot {
	return &state.Snapshot{Version: state.Version, Databases: map[string]state.Database{}}
}

// Le state doit être écrit APRÈS CHAQUE création, pas une fois à la fin : un
// arrêt en cours de route laisse alors un state exactement vrai, et aucune
// database créée sans ancre.
func TestRunSavesStateAfterEachCreation(t *testing.T) {
	dir := t.TempDir()
	p := &diff.Plan{Changes: []diff.Change{
		createChange("projects", "Projects"),
		createChange("tasks", "Tasks"),
	}}

	var savedBeforeSecond bool
	n := 0
	creator := creatorFunc(func(context.Context, []byte) (resources.CreatedDatabase, error) {
		if n == 1 {
			// Au moment du DEUXIÈME appel, la première doit déjà être sur disque.
			loaded, err := state.Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			_, savedBeforeSecond = loaded.Databases["projects"]
		}
		n++
		if n == 1 {
			return createdFrom("db-1", "Projects"), nil
		}
		return createdFrom("db-2", "Tasks"), nil
	})

	rep, err := Run(context.Background(), p, emptySnapshot(), Options{
		Dir: dir, ParentPageID: testParentPageID, Creator: creator,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !savedBeforeSecond {
		t.Error("le state n'était pas sur disque avant la deuxième création")
	}
	if len(rep.Created) != 2 {
		t.Errorf("Created = %v, want 2 entrées", rep.Created)
	}
	if !rep.Converged() {
		t.Errorf("Converged() = false, want true : %+v", rep)
	}
}

// Un échec au deuxième POST laisse la première création dans le state, et le
// message doit nommer les deux — ce qui est acquis, et ce qui ne l'est pas.
func TestRunKeepsFirstCreationWhenSecondFails(t *testing.T) {
	dir := t.TempDir()
	p := &diff.Plan{Changes: []diff.Change{
		createChange("projects", "Projects"),
		createChange("tasks", "Tasks"),
	}}

	n := 0
	creator := creatorFunc(func(context.Context, []byte) (resources.CreatedDatabase, error) {
		n++
		if n == 1 {
			return createdFrom("db-1", "Projects"), nil
		}
		return resources.CreatedDatabase{}, &transport.APIError{
			Status: 400, NotionCode: "validation_error", Message: "Invalid property.",
		}
	})

	_, err := Run(context.Background(), p, emptySnapshot(), Options{
		Dir: dir, ParentPageID: testParentPageID, Creator: creator,
	})
	if err == nil {
		t.Fatal("Run() error = nil, want l'échec de la deuxième création")
	}
	for _, want := range []string{"database.tasks", "database.projects", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, il doit contenir %q", err.Error(), want)
		}
	}
	loaded, lerr := state.Load(dir)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if _, ok := loaded.Databases["projects"]; !ok {
		t.Error("database.projects doit rester dans le state : elle existe vraiment")
	}
	if _, ok := loaded.Databases["tasks"]; ok {
		t.Error("database.tasks ne doit pas être dans le state : elle n'a pas été créée")
	}
}

// Une issue inconnue arrête net : on ne sait pas si la mutation a été
// appliquée, donc enchaîner travaillerait sur un workspace indéterminé.
func TestRunStopsOnUnknownOutcomeAndPointsToImport(t *testing.T) {
	dir := t.TempDir()
	p := &diff.Plan{Changes: []diff.Change{
		createChange("projects", "Projects"),
		createChange("tasks", "Tasks"),
	}}

	calls := 0
	creator := creatorFunc(func(context.Context, []byte) (resources.CreatedDatabase, error) {
		calls++
		return resources.CreatedDatabase{}, &transport.OutcomeUnknownError{Cause: context.DeadlineExceeded}
	})

	_, err := Run(context.Background(), p, emptySnapshot(), Options{
		Dir: dir, ParentPageID: testParentPageID, Creator: creator,
	})
	if err == nil {
		t.Fatal("Run() error = nil, want l'issue inconnue")
	}
	if calls != 1 {
		t.Errorf("appels = %d, want 1 : on n'enchaîne pas après une issue inconnue", calls)
	}
	for _, want := range []string{"database.projects", "Projects", "import", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, il doit contenir %q", err.Error(), want)
		}
	}
}

// L'identité survit à une relecture ratée : la perdre coûterait un doublon.
func TestRunKeepsIdentityWhenReadBackFails(t *testing.T) {
	dir := t.TempDir()
	p := &diff.Plan{Changes: []diff.Change{createChange("projects", "Projects")}}

	creator := creatorFunc(func(context.Context, []byte) (resources.CreatedDatabase, error) {
		return resources.CreatedDatabase{
			ID: "db-1", DataSourceID: "ds-1", ReadErr: os.ErrDeadlineExceeded,
		}, nil
	})

	_, err := Run(context.Background(), p, emptySnapshot(), Options{
		Dir: dir, ParentPageID: testParentPageID, Creator: creator,
	})
	if err == nil {
		t.Fatal("Run() error = nil, want le signalement de la relecture ratée")
	}
	if !strings.Contains(err.Error(), "import") {
		t.Errorf("message = %q, il doit indiquer la resynchronisation par import", err.Error())
	}
	loaded, lerr := state.Load(dir)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if got := loaded.Databases["projects"]; got.ID != "db-1" || got.DataSourceID != "ds-1" {
		t.Errorf("state = %+v, want l'identité db-1/ds-1 conservée", got)
	}
}

// Une entrée de state obsolète est retirée : ça n'écrit rien dans Notion et ça
// fait converger le plan.
func TestRunCleansStaleStateEntries(t *testing.T) {
	dir := t.TempDir()
	snap := &state.Snapshot{
		Version:   state.Version,
		Databases: map[string]state.Database{"tasks": {ID: "db-1", Name: "Tasks"}},
	}
	p := &diff.Plan{StaleState: []string{"database.tasks"}}

	rep, err := Run(context.Background(), p, snap, Options{
		Dir: dir, ParentPageID: testParentPageID, Creator: refuseToCreate(t),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(rep.Cleaned) != 1 {
		t.Errorf("Cleaned = %v, want 1 entrée", rep.Cleaned)
	}
	loaded, lerr := state.Load(dir)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if _, ok := loaded.Databases["tasks"]; ok {
		t.Error("l'entrée obsolète n'a pas été retirée")
	}
}

// Ce qu'apply ne sait pas écrire est nommé, pas exécuté à moitié.
func TestRunSkipsChangesItCannotWrite(t *testing.T) {
	dir := t.TempDir()
	p := &diff.Plan{Changes: []diff.Change{
		{Resource: "database.tasks", Key: "tasks", Kind: resources.KindUpdate},
	}}

	rep, err := Run(context.Background(), p, emptySnapshot(), Options{
		Dir: dir, ParentPageID: testParentPageID, Creator: refuseToCreate(t),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(rep.Skipped) != 1 || !strings.Contains(rep.Skipped[0], "database.tasks") {
		t.Errorf("Skipped = %v, want database.tasks", rep.Skipped)
	}
	if rep.Converged() {
		t.Error("Converged() = true alors qu'un changement n'a pas été appliqué")
	}
}

// L'API n'a pas écrit ce qui était annoncé : la création reste acquise, le
// state reste vrai, mais l'écart est rapporté. C'est la thèse du produit
// appliquée à notre propre écriture.
func TestRunReportsMismatchBetweenTargetAndReality(t *testing.T) {
	dir := t.TempDir()
	target := &state.Database{
		Name: "Projects",
		Properties: map[string]state.Property{
			"Name":   {Type: "title"},
			"Budget": {Type: "number", Format: "euro"},
		},
	}
	p := &diff.Plan{Changes: []diff.Change{{
		Resource: "database.projects", Key: "projects",
		Kind: resources.KindCreate, Target: target,
	}}}

	// L'API rend une database SANS la propriété Budget.
	creator := creatorFunc(func(context.Context, []byte) (resources.CreatedDatabase, error) {
		return createdFrom("db-1", "Projects"), nil
	})

	rep, err := Run(context.Background(), p, emptySnapshot(), Options{
		Dir: dir, ParentPageID: testParentPageID, Creator: creator,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(rep.Created) != 1 {
		t.Errorf("Created = %v, want la création acquise", rep.Created)
	}
	joined := strings.Join(rep.Mismatches, "\n")
	if !strings.Contains(joined, "Budget") {
		t.Errorf("Mismatches = %v, il doit nommer Budget", rep.Mismatches)
	}
	if rep.Converged() {
		t.Error("Converged() = true malgré un écart entre la cible et le réel")
	}
}

// Une création conforme ne doit produire AUCUN écart : sans ce test, un
// comparateur trop bavard ferait échouer tous les apply réussis.
func TestRunReportsNoMismatchWhenAPIWroteWhatWasAnnounced(t *testing.T) {
	dir := t.TempDir()
	p := &diff.Plan{Changes: []diff.Change{createChange("projects", "Projects")}}

	creator := creatorFunc(func(context.Context, []byte) (resources.CreatedDatabase, error) {
		return createdFrom("db-1", "Projects"), nil
	})

	rep, err := Run(context.Background(), p, emptySnapshot(), Options{
		Dir: dir, ParentPageID: testParentPageID, Creator: creator,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(rep.Mismatches) != 0 {
		t.Errorf("Mismatches = %v, want vide", rep.Mismatches)
	}
}

// Le payload part de la CIBLE, pas de la configuration : c'est l'invariant que
// tout le reste protège. On le vérifie sur ce que le créateur reçoit vraiment.
func TestRunSendsThePayloadBuiltFromTheTarget(t *testing.T) {
	dir := t.TempDir()
	target := &state.Database{
		Name: "Projects",
		Properties: map[string]state.Property{
			"Statut": {Type: "status", Options: []state.Option{
				{Key: "todo", Name: "À faire", Group: "In progress"},
			}},
		},
	}
	p := &diff.Plan{Changes: []diff.Change{{
		Resource: "database.projects", Key: "projects",
		Kind: resources.KindCreate, Target: target,
	}}}

	var sent string
	creator := creatorFunc(func(_ context.Context, body []byte) (resources.CreatedDatabase, error) {
		sent = string(body)
		return createdFrom("db-1", "Projects"), nil
	})

	if _, err := Run(context.Background(), p, emptySnapshot(), Options{
		Dir: dir, ParentPageID: testParentPageID, Creator: creator,
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// Le group déclaré part tel quel, et surtout : aucun "To-do" substitué.
	if !strings.Contains(sent, `"group":"In progress"`) {
		t.Errorf("payload = %s, il doit porter le group déclaré", sent)
	}
	if strings.Contains(sent, `"To-do"`) {
		t.Errorf("payload = %s, aucun groupe ne doit être substitué", sent)
	}
	if !strings.Contains(sent, testParentPageID) {
		t.Errorf("payload = %s, il doit viser la page parente", sent)
	}
}
