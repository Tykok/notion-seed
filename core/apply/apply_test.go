// SPDX-License-Identifier: GPL-3.0-or-later

package apply

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
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

// Un plan bloqué ne doit JAMAIS être écrit, et la garde doit vivre dans le
// paquet qui écrit — pas seulement dans la commande. Un plan peut être bloqué
// par une autre ressource que celles qu'il s'apprête à créer : sans cette
// garde, un second appelant écrirait les créations d'un plan refusé.
func TestRunRefusesABlockedPlan(t *testing.T) {
	dir := t.TempDir()
	p := &diff.Plan{
		Changes:        []diff.Change{createChange("projects", "Projects")},
		Blocked:        true,
		BlockedReasons: []string{"database.tasks : réécriture silencieuse"},
	}

	_, err := Run(context.Background(), p, emptySnapshot(), Options{
		Dir: dir, ParentPageID: testParentPageID, Creator: refuseToCreate(t),
	})
	if err == nil {
		t.Fatal("Run() error = nil, want le refus d'un plan bloqué")
	}
	if !strings.Contains(err.Error(), "  → ") {
		t.Errorf("message = %q, il doit porter une action corrective", err.Error())
	}
	if _, serr := os.Stat(state.Path(dir)); !os.IsNotExist(serr) {
		t.Error("un state a été écrit malgré un plan bloqué")
	}
}

// Le message d'issue inconnue doit nommer la page parente : l'utilisateur va y
// aller vérifier, au moment précis où son workspace est indéterminé. Aller
// rechercher l'id dans workspace.yaml est un travail que la commande peut faire.
func TestRunUnknownOutcomeNamesTheParentPage(t *testing.T) {
	dir := t.TempDir()
	p := &diff.Plan{Changes: []diff.Change{createChange("projects", "Projects")}}

	creator := creatorFunc(func(context.Context, []byte) (resources.CreatedDatabase, error) {
		return resources.CreatedDatabase{}, &transport.OutcomeUnknownError{Cause: context.DeadlineExceeded}
	})

	_, err := Run(context.Background(), p, emptySnapshot(), Options{
		Dir: dir, ParentPageID: testParentPageID, Creator: creator,
	})
	if err == nil {
		t.Fatal("Run() error = nil, want l'issue inconnue")
	}
	if !strings.Contains(err.Error(), testParentPageID) {
		t.Errorf("message = %q, il doit nommer la page parente %s", err.Error(), testParentPageID)
	}
}

// Une option que l'API a écrite EN TROP doit se lire comme telle. Réutiliser la
// note du plan telle quelle donnait « l'API n'a pas écrit - option "Fait" —
// absente du YAML », qui décrit l'inverse de ce qui s'est passé.
func TestRunReportsExtraOptionAsWrittenInExcess(t *testing.T) {
	dir := t.TempDir()
	target := &state.Database{
		Name: "Tasks",
		Properties: map[string]state.Property{
			"Statut": {Type: "select", Options: []state.Option{
				{Key: "todo", Name: "À faire"},
			}},
		},
	}
	p := &diff.Plan{Changes: []diff.Change{{
		Resource: "database.tasks", Key: "tasks",
		Kind: resources.KindCreate, Target: target,
	}}}

	// L'API rend une option de plus que ce qui était annoncé.
	creator := creatorFunc(func(context.Context, []byte) (resources.CreatedDatabase, error) {
		return resources.CreatedDatabase{
			ID: "db-1", DataSourceID: "ds-1",
			Remote: resources.RemoteDatabase{
				ID: "db-1", DataSourceID: "ds-1", Name: "Tasks", Found: true,
				Properties: map[string]resources.RemoteProperty{
					"Statut": {ID: "p1", Type: "select", Options: []resources.RemoteOption{
						{ID: "o1", Name: "À faire"},
						{ID: "o2", Name: "Fait"},
					}},
				},
			},
		}, nil
	})

	rep, err := Run(context.Background(), p, emptySnapshot(), Options{
		Dir: dir, ParentPageID: testParentPageID, Creator: creator,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	joined := strings.Join(rep.Mismatches, "\n")
	if !strings.Contains(joined, "Fait") {
		t.Fatalf("Mismatches = %v, il doit nommer l'option en trop", rep.Mismatches)
	}
	if strings.Contains(joined, "n'a pas écrit") {
		t.Errorf("écart = %q : une option écrite en trop ne peut pas se lire "+
			"« l'API n'a pas écrit »", joined)
	}
	if !strings.Contains(joined, "en trop") {
		t.Errorf("écart = %q, il doit dire que l'option est en trop", joined)
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

// Review Focus #3 : Withheld vide EST l'autorisation d'écrire. Une modification
// autorisée sans cible est un défaut interne : la sauter ferait converger un
// apply qui n'a pas écrit ce que le plan montrait, et s'arrêter sur elle
// laisserait écrite la création qui la précède. Le plan entier est refusé, avant
// le premier appel.
func TestRunRefusesAnAuthorizedChangeWithoutTargetBeforeAnyWrite(t *testing.T) {
	dir := t.TempDir()
	p := &diff.Plan{Changes: []diff.Change{
		createChange("projects", "Projects"),
		{Resource: "database.tasks", Key: "tasks", Kind: resources.KindUpdate},
	}}

	_, err := Run(context.Background(), p, emptySnapshot(), Options{
		Dir: dir, ParentPageID: testParentPageID, Creator: refuseToCreate(t),
	})
	if err == nil {
		t.Fatal("Run() error = nil, want le refus d'un changement autorisé sans cible")
	}
	for _, want := range []string{"database.tasks", "défaut interne", "rien n'a été appliqué", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, il doit contenir %q", err.Error(), want)
		}
	}
	if _, serr := os.Stat(state.Path(dir)); !os.IsNotExist(serr) {
		t.Error("un state a été écrit alors que le plan est refusé")
	}
}

// Une destruction n'a pas de cible : son identité vient du state. Sans id, le
// PATCH viserait "/v1/databases/", qui ne désigne rien.
func TestCheckRefusesADestroyWithoutAnIdentityInTheState(t *testing.T) {
	p := &diff.Plan{Changes: []diff.Change{
		{Resource: "database.tasks", Key: "tasks", Kind: resources.KindDestroy},
	}}
	snap := emptySnapshot()
	snap.Databases["tasks"] = state.Database{Name: "Tasks"}

	err := Check(p, snap)
	if err == nil {
		t.Fatal("Check() error = nil, want un refus")
	}
	for _, want := range []string{"database.tasks", "défaut interne", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, il doit contenir %q", err.Error(), want)
		}
	}

	snap.Databases["tasks"] = state.Database{ID: "db-1", Name: "Tasks"}
	if err := Check(p, snap); err != nil {
		t.Errorf("Check() error = %v, want nil : l'identité est dans le state", err)
	}
}

// Une ressource retenue n'a pas de cible, et c'est voulu : Check la laisse
// passer, Run la nommera sans l'écrire.
func TestCheckLetsAWithheldChangeThrough(t *testing.T) {
	c := updateChange("tasks", nil, prioDetail)
	c.Withheld = "une option doit être migrée à la main"
	if err := Check(&diff.Plan{Changes: []diff.Change{c}}, emptySnapshot()); err != nil {
		t.Errorf("Check() error = %v, want nil", err)
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

// fakeUpdater enregistre ce qu'on lui demande d'écrire.
//
// err survient à l'appel d'index failAt (les précédents réussissent). dbFails
// dit si l'échec touche le PATCH database — sinon, c'est le PATCH data source
// qui échoue, et la database est écrite si un corps lui était destiné.
type fakeUpdater struct {
	dbBodies [][]byte
	dsBodies [][]byte
	remote   resources.RemoteDatabase
	readErr  error

	err     error
	failAt  int
	dbFails bool

	exists    bool
	existsErr error
	probes    int
}

func (f *fakeUpdater) Update(_ context.Context, _, _ string, dbBody, dsBody []byte) (resources.UpdatedDatabase, error) {
	call := len(f.dbBodies)
	f.dbBodies = append(f.dbBodies, dbBody)
	f.dsBodies = append(f.dsBodies, dsBody)
	if f.err != nil && call == f.failAt {
		return resources.UpdatedDatabase{DatabaseWritten: !f.dbFails && len(dbBody) > 0}, f.err
	}
	return resources.UpdatedDatabase{
		DatabaseWritten: len(dbBody) > 0, Remote: f.remote, ReadErr: f.readErr,
	}, nil
}

func (f *fakeUpdater) DatabaseExists(_ context.Context, _ string) (bool, error) {
	f.probes++
	return f.exists, f.existsErr
}

func (f *fakeUpdater) calls() int { return len(f.dbBodies) + f.probes }

func prioTarget() *state.Database {
	return &state.Database{
		ID: "db-1", DataSourceID: "ds-1", Name: "Tasks",
		Properties: map[string]state.Property{
			"Prio":  {Type: "select"},
			"Notes": {Type: "rich_text"},
		},
	}
}

func prioRemote() resources.RemoteDatabase {
	return resources.RemoteDatabase{
		ID: "db-1", DataSourceID: "ds-1", Name: "Tasks", Found: true,
		Properties: map[string]resources.RemoteProperty{
			"Prio":  {ID: "p1", Type: "select"},
			"Notes": {ID: "n1", Type: "rich_text"},
		},
	}
}

func updateChange(key string, target *state.Database, details ...resources.Detail) diff.Change {
	return diff.Change{
		Resource: "database." + key, Key: key,
		Kind: resources.KindUpdate, Target: target, Details: details,
	}
}

var (
	prioDetail = resources.Detail{Op: "~", Target: `property "Prio"`, Property: "Prio"}
	nameDetail = resources.Detail{Op: "~", Target: "name", Field: "name"}
)

func TestRunWritesOnlyThePropertiesThePlanShows(t *testing.T) {
	dir := t.TempDir()
	up := &fakeUpdater{remote: prioRemote()}
	p := &diff.Plan{Changes: []diff.Change{updateChange("tasks", prioTarget(), prioDetail)}}

	rep, err := Run(context.Background(), p, emptySnapshot(), Options{Dir: dir, Updater: up})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(rep.Updated) != 1 {
		t.Fatalf("Updated = %v, want 1 ligne", rep.Updated)
	}
	body := string(up.dsBodies[0])
	if !strings.Contains(body, "Prio") {
		t.Errorf("payload = %s, want Prio", body)
	}
	if strings.Contains(body, "Notes") {
		t.Errorf("payload = %s, want sans Notes : le plan ne la montre pas", body)
	}
	if up.dbBodies[0] != nil {
		t.Errorf("dbBody = %s, want nil : aucun champ de database dans le plan", up.dbBodies[0])
	}
	if len(rep.Mismatches) != 0 {
		t.Errorf("Mismatches = %v, want vide", rep.Mismatches)
	}

	// Le state est sauvé depuis la RELECTURE : l'id de propriété n'existe que là.
	loaded, lerr := state.Load(dir)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if got := loaded.Databases["tasks"].Properties["Prio"].ID; got != "p1" {
		t.Errorf("state Prio.ID = %q, want p1 (relu)", got)
	}
}

// Review Focus #3 : un update qui ne touche qu'un champ n'appelle pas le data
// source.
func TestRunWritesOnlyTheDatabaseWhenNoPropertyChanges(t *testing.T) {
	dir := t.TempDir()
	target := &state.Database{ID: "db-1", DataSourceID: "ds-1", Name: "Tâches"}
	up := &fakeUpdater{remote: resources.RemoteDatabase{
		ID: "db-1", DataSourceID: "ds-1", Name: "Tâches", Found: true,
	}}
	p := &diff.Plan{Changes: []diff.Change{updateChange("tasks", target, nameDetail)}}

	if _, err := Run(context.Background(), p, emptySnapshot(), Options{Dir: dir, Updater: up}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(up.dbBodies) != 1 {
		t.Fatalf("%d appel(s) Update, want 1", len(up.dbBodies))
	}
	if up.dsBodies[0] != nil {
		t.Errorf("dsBody = %s, want nil", up.dsBodies[0])
	}
	if !strings.Contains(string(up.dbBodies[0]), "Tâches") {
		t.Errorf("dbBody = %s, want le nouveau titre", up.dbBodies[0])
	}
}

// L'équivalence « plan ≡ payload » a deux sens. Le test précédent couvre
// l'inclusion : rien ne part qui ne soit dans le plan. Celui-ci couvre l'autre :
// rien du plan ne reste au sol.
func TestWriteSetCoversEveryDetail(t *testing.T) {
	details := []resources.Detail{
		{Op: "~", Target: "name", Field: "name"},
		{Op: "~", Target: "icon", Field: "icon"},
		{Op: "~", Target: `property "Prio"`, Property: "Prio"},
		{Op: "-", Target: `option "Basse" (propriété "Prio")`, Property: "Prio"},
		{Op: "+", Target: `property "Neuve"`, Property: "Neuve"},
	}
	fields, props := writeSet(details)

	for _, d := range details {
		switch {
		case d.Field != "":
			if !slices.Contains(fields, d.Field) {
				t.Errorf("champ %q du plan absent du jeu d'écriture", d.Field)
			}
		case d.Property != "":
			if !slices.Contains(props, d.Property) {
				t.Errorf("propriété %q du plan absente du jeu d'écriture", d.Property)
			}
		default:
			t.Errorf("détail %q sans Field ni Property : il ne peut pas être écrit", d.Target)
		}
	}
	// Dédoublonné : "Prio" porte deux lignes, une seule écriture.
	if len(props) != 2 {
		t.Errorf("props = %v, want 2 (Prio dédoublonnée, Neuve)", props)
	}
}

// Les lignes d'option sous une propriété neuve ou changée de type disent ce qui
// part ; elles n'ajoutent AUCUNE écriture : le jeu d'écriture est celui des
// seules lignes de propriété.
func TestOptionLinesOfANewPropertyAddNoWrite(t *testing.T) {
	actual := state.Database{
		ID: "db-1", DataSourceID: "ds-1", Name: "Tasks",
		Properties: map[string]state.Property{
			"Name": {ID: "title", Type: "title"},
			"Prio": {ID: "p1", Type: "status", Options: []state.Option{
				{ID: "o-haute", Name: "Haute"},
			}},
		},
	}
	applied := actual
	desired := state.Database{Properties: map[string]state.Property{
		"Name": {Type: "title"},
		"Prio": {Type: "select", Options: []state.Option{{Name: "Haute", Color: "red"}}},
		"Etat": {Type: "select", Options: []state.Option{
			{Name: "Ouvert", Color: "green"}, {Name: "Clos"},
		}},
	}}
	res := diff.CompareDatabase("tasks", &desired, &applied, &actual)

	var withoutOptions []resources.Detail
	optionLines := 0
	for _, d := range res.Changeset.Details {
		if strings.HasPrefix(d.Target, "option ") {
			optionLines++
			continue
		}
		withoutOptions = append(withoutOptions, d)
	}
	if optionLines != 3 {
		t.Fatalf("%d lignes d'option, want 3 :\n%v", optionLines, res.Changeset.Details)
	}
	_, got := writeSet(res.Changeset.Details)
	_, want := writeSet(withoutOptions)
	if !slices.Equal(got, want) {
		t.Errorf("jeu d'écriture = %v, want %v : les lignes d'option n'écrivent rien de plus", got, want)
	}
	if !slices.Equal(got, []string{"Etat", "Prio"}) {
		t.Errorf("jeu d'écriture = %v, want [Etat Prio]", got)
	}
}

// Le premier piège du §1 : l'API remplace la liste entière des options, et une
// option existante envoyée sans son id est détruite puis recréée. Chaque pièce
// est couverte seule ; ce test verrouille leur COMPOSITION — du plan jusqu'au
// corps envoyé —, qu'un second constructeur de cible ou une copie de Target
// perdant l'id casserait sans qu'aucun test unitaire ne bouge.
func TestRunSendsRemoteOptionIDsFromThePlanToThePayload(t *testing.T) {
	actual := state.Database{
		ID: "db-1", DataSourceID: "ds-1", Name: "Tasks",
		Properties: map[string]state.Property{
			"Name": {ID: "title", Type: "title"},
			"Prio": {ID: "p1", Type: "select", Options: []state.Option{
				{ID: "o-haute", Name: "Haute", Color: "red"},
				{ID: "o-basse", Name: "Basse", Color: "blue"},
			}},
		},
	}
	applied := state.Database{
		ID: "db-1", DataSourceID: "ds-1", Name: "Tasks",
		Properties: map[string]state.Property{
			"Name": {ID: "title", Type: "title"},
			"Prio": {ID: "p1", Type: "select", Options: []state.Option{
				{ID: "o-haute", Key: "haute", Name: "Haute", Color: "red"},
				{ID: "o-basse", Key: "basse", Name: "Basse", Color: "blue"},
			}},
		},
	}
	// "Haute" reste, "Moyenne" arrive, "Basse" n'est plus réclamée.
	desired := state.Database{Properties: map[string]state.Property{
		"Name": {Type: "title"},
		"Prio": {Type: "select", Options: []state.Option{
			{Key: "haute", Name: "Haute", Color: "red"},
			{Key: "moyenne", Name: "Moyenne", Color: "orange"},
		}},
	}}
	res := diff.CompareDatabase("tasks", &desired, &applied, &actual)
	if res.Withheld != "" || res.Target == nil {
		t.Fatalf("montage faux : Withheld=%q Target=%v", res.Withheld, res.Target)
	}
	c := updateChange("tasks", res.Target, res.Changeset.Details...)

	up := &fakeUpdater{remote: prioRemote()}
	snap := emptySnapshot()
	snap.Databases["tasks"] = applied
	if _, err := Run(context.Background(), &diff.Plan{Changes: []diff.Change{c}}, snap,
		Options{Dir: t.TempDir(), Updater: up}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(up.dsBodies) != 1 || up.dsBodies[0] == nil {
		t.Fatalf("dsBodies = %v, want un PATCH data source", up.dsBodies)
	}

	var body struct {
		Properties map[string]struct {
			Select struct {
				Options []map[string]any `json:"options"`
			} `json:"select"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(up.dsBodies[0], &body); err != nil {
		t.Fatalf("corps illisible %s : %v", up.dsBodies[0], err)
	}
	if _, sent := body.Properties["Name"]; sent {
		t.Errorf("payload = %s : Name n'est pas dans le plan", up.dsBodies[0])
	}
	byName := map[string]map[string]any{}
	for _, o := range body.Properties["Prio"].Select.Options {
		byName[o["name"].(string)] = o
	}
	if got := byName["Haute"]["id"]; got != "o-haute" {
		t.Errorf("Haute part avec l'id %v, want o-haute : sans lui, l'API la recrée", got)
	}
	moyenne, ok := byName["Moyenne"]
	if !ok {
		t.Fatalf("payload = %s : l'option neuve Moyenne manque", up.dsBodies[0])
	}
	if id, has := moyenne["id"]; has {
		t.Errorf("Moyenne part avec l'id %v, want aucun : elle est neuve", id)
	}
	if _, sent := byName["Basse"]; sent {
		t.Errorf("payload = %s : Basse n'est pas réclamée, elle ne doit pas partir", up.dsBodies[0])
	}
	if len(byName) != 2 {
		t.Errorf("options envoyées = %v, want exactement Haute et Moyenne", byName)
	}
}

func TestRunSkipsAWithheldResourceWithoutCallingTheAPI(t *testing.T) {
	dir := t.TempDir()
	up := &fakeUpdater{}
	c := updateChange("tasks", nil, prioDetail)
	c.Withheld = "une option doit être migrée à la main"
	p := &diff.Plan{Changes: []diff.Change{c}}

	rep, err := Run(context.Background(), p, emptySnapshot(), Options{Dir: dir, Updater: up})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if up.calls() != 0 {
		t.Error("un appel a eu lieu sur une ressource retenue")
	}
	if len(rep.Skipped) != 1 || rep.Skipped[0] != "database.tasks" {
		t.Errorf("Skipped = %v, want [database.tasks]", rep.Skipped)
	}
}

// Review Focus #2 : sans data_source_id, le PATCH viserait
// "/v1/data_sources/", qui ne désigne rien. La cible vient toujours d'une
// relecture fraîche : un id vide est un défaut interne, refusé avant tout appel.
func TestRunRefusesAnUpdateWithoutADataSourceID(t *testing.T) {
	dir := t.TempDir()
	target := prioTarget()
	target.DataSourceID = ""
	up := &fakeUpdater{}
	p := &diff.Plan{Changes: []diff.Change{updateChange("tasks", target, prioDetail)}}

	_, err := Run(context.Background(), p, emptySnapshot(), Options{Dir: dir, Updater: up})
	if err == nil {
		t.Fatal("error = nil, want un refus avant tout appel")
	}
	if up.calls() != 0 {
		t.Error("un appel a eu lieu malgré l'absence de data_source_id")
	}
	for _, want := range []string{"database.tasks", "défaut interne", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("erreur = %q, want contenant %q", err, want)
		}
	}
}

// La CLI ne branche l'Updater qu'à la tâche suivante : un update sans Updater
// doit être un défaut nommé, pas un déréférencement nil.
func TestRunRefusesAnUpdateWithoutAnUpdater(t *testing.T) {
	p := &diff.Plan{Changes: []diff.Change{updateChange("tasks", prioTarget(), prioDetail)}}

	_, err := Run(context.Background(), p, emptySnapshot(), Options{Dir: t.TempDir()})
	if err == nil {
		t.Fatal("error = nil, want un défaut interne nommé")
	}
	for _, want := range []string{"database.tasks", "défaut interne", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("erreur = %q, want contenant %q", err, want)
		}
	}
}

// Le 404 du PATCH data source accuse le partage avec l'intégration, alors que la
// cause peut être un ancêtre archivé. Sonder la database tranche.
func TestRunDiagnosesAnArchivedAncestorOnA404(t *testing.T) {
	up := &fakeUpdater{
		err: &transport.APIError{Status: 404, NotionCode: "object_not_found",
			Message: "Could not find data_source. Make sure the relevant pages and " +
				"databases are shared with your integration"},
		exists: true,
	}
	p := &diff.Plan{Changes: []diff.Change{updateChange("tasks", prioTarget(), prioDetail)}}

	_, err := Run(context.Background(), p, emptySnapshot(), Options{Dir: t.TempDir(), Updater: up})
	if err == nil {
		t.Fatal("error = nil, want une erreur diagnostiquée")
	}
	if up.probes != 1 {
		t.Errorf("%d sonde(s), want 1", up.probes)
	}
	for _, want := range []string{"ancêtre", "corbeille", "restaurez la page parente", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("erreur = %q, want contenant %q", err, want)
		}
	}
	for _, unwanted := range []string{"intégration", "integration"} {
		if strings.Contains(err.Error(), unwanted) {
			t.Errorf("erreur = %q, want SANS le conseil de partage, qui est faux ici", err)
		}
	}
}

// Un ancêtre à la corbeille fait échouer le PATCH database le PREMIER. Quand ce
// PATCH est passé, un 404 du data source ne peut donc pas venir d'un ancêtre
// archivé, même si la database se lit encore : conseiller de restaurer la page
// parente enverrait l'utilisateur chercher une cause qui n'existe pas.
func TestRunDoesNotBlameAnArchivedAncestorAfterADatabaseWrite(t *testing.T) {
	up := &fakeUpdater{
		err: &transport.APIError{Status: 404, NotionCode: "object_not_found",
			Message: "Could not find data_source"},
		exists: true,
	}
	p := &diff.Plan{Changes: []diff.Change{updateChange("tasks", prioTarget(), nameDetail, prioDetail)}}

	_, err := Run(context.Background(), p, emptySnapshot(), Options{Dir: t.TempDir(), Updater: up})
	if err == nil {
		t.Fatal("error = nil, want une erreur diagnostiquée")
	}
	if len(up.dbBodies) != 1 || up.dbBodies[0] == nil {
		t.Fatalf("montage faux : le PATCH database devait partir")
	}
	for _, unwanted := range []string{"ancêtre", "corbeille", "restaurez la page parente"} {
		if strings.Contains(err.Error(), unwanted) {
			t.Errorf("erreur = %q, want SANS %q : le PATCH database réussi l'exclut", err, unwanted)
		}
	}
	for _, want := range []string{"data source", "partagé", "déjà écrit sur cette ressource : le nom", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("erreur = %q, want contenant %q", err, want)
		}
	}
}

func TestRunDiagnosesAVanishedDatabaseOnA404(t *testing.T) {
	up := &fakeUpdater{
		err: &transport.APIError{Status: 404, NotionCode: "object_not_found",
			Message: "Could not find data_source"},
		exists: false,
	}
	p := &diff.Plan{Changes: []diff.Change{updateChange("tasks", prioTarget(), prioDetail)}}

	_, err := Run(context.Background(), p, emptySnapshot(), Options{Dir: t.TempDir(), Updater: up})
	if err == nil {
		t.Fatal("error = nil, want une erreur diagnostiquée")
	}
	for _, want := range []string{"disparu", "partagée avec l'intégration", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("erreur = %q, want contenant %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "corbeille") {
		t.Errorf("erreur = %q, want SANS le diagnostic d'ancêtre archivé", err)
	}
}

// Si la sonde elle-même échoue, on ne tranche pas — mais on ne relaie pas
// davantage le conseil de partage du 404, qui peut être faux.
func TestRunFallsBackWhenTheProbeFails(t *testing.T) {
	up := &fakeUpdater{
		err: &transport.APIError{Status: 404, NotionCode: "object_not_found",
			Message: "Make sure the relevant pages are shared with your integration"},
		existsErr: errors.New("réseau coupé"),
	}
	p := &diff.Plan{Changes: []diff.Change{updateChange("tasks", prioTarget(), prioDetail)}}

	_, err := Run(context.Background(), p, emptySnapshot(), Options{Dir: t.TempDir(), Updater: up})
	if err == nil {
		t.Fatal("error = nil")
	}
	for _, want := range []string{"réseau coupé", "404", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("erreur = %q, want contenant %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "shared with your integration") {
		t.Errorf("erreur = %q, want sans le texte brut du 404", err)
	}
}

// Mesuré : sur un ancêtre archivé, le PATCH database échoue le premier, avec un
// 400 qui nomme la cause. Le message dit le remède.
func TestRunNamesAnArchivedAncestorOnTheDatabasePatch(t *testing.T) {
	target := prioTarget()
	up := &fakeUpdater{
		err: &transport.APIError{Status: 400, NotionCode: "validation_error",
			Message: "Can't edit page on block with an archived ancestor. You must " +
				"unarchive the ancestor before editing page."},
		dbFails: true,
	}
	p := &diff.Plan{Changes: []diff.Change{updateChange("tasks", target, nameDetail, prioDetail)}}

	_, err := Run(context.Background(), p, emptySnapshot(), Options{Dir: t.TempDir(), Updater: up})
	if err == nil {
		t.Fatal("error = nil")
	}
	for _, want := range []string{"restaurez la page parente", "rien n'a été écrit sur cette ressource"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("erreur = %q, want contenant %q", err, want)
		}
	}
}

// Échec du second PATCH : le message nomme EXACTEMENT les champs écrits, dit
// qu'aucune donnée n'est touchée, et liste ce qui était acquis avant.
func TestRunNamesWhatPassedWhenTheSecondPatchFails(t *testing.T) {
	dir := t.TempDir()
	up := &fakeUpdater{
		remote: prioRemote(),
		err:    &transport.APIError{Status: 400, NotionCode: "validation_error", Message: "boom"},
		failAt: 1,
	}
	other := prioTarget()
	other.ID, other.DataSourceID = "db-2", "ds-2"
	p := &diff.Plan{Changes: []diff.Change{
		updateChange("tasks", prioTarget(), prioDetail),
		updateChange("projects", other, nameDetail, prioDetail),
	}}

	rep, err := Run(context.Background(), p, emptySnapshot(), Options{Dir: dir, Updater: up})
	if err == nil {
		t.Fatal("error = nil, want l'échec du second PATCH")
	}
	if len(rep.Updated) != 1 {
		t.Errorf("Updated = %v, want la première modification acquise", rep.Updated)
	}
	msg := err.Error()
	for _, want := range []string{"database.projects", "le nom", "aucune donnée", "database.tasks", "  → "} {
		if !strings.Contains(msg, want) {
			t.Errorf("erreur = %q, want contenant %q", msg, want)
		}
	}
	for _, unwanted := range []string{"icône", "description"} {
		if strings.Contains(msg, unwanted) {
			t.Errorf("erreur = %q : %q n'a pas été envoyé, il ne doit pas être nommé", msg, unwanted)
		}
	}
}

// Issue inconnue : arrêt net, sans enchaîner. `plan` suffit à voir le réel —
// contrairement à la création, il n'y a pas d'identité à ré-adopter.
func TestRunStopsOnAnUnknownOutcome(t *testing.T) {
	up := &fakeUpdater{err: &transport.OutcomeUnknownError{Cause: errors.New("timeout")}}
	other := prioTarget()
	other.ID, other.DataSourceID = "db-2", "ds-2"
	p := &diff.Plan{Changes: []diff.Change{
		updateChange("tasks", prioTarget(), prioDetail),
		updateChange("projects", other, prioDetail),
	}}

	_, err := Run(context.Background(), p, emptySnapshot(), Options{Dir: t.TempDir(), Updater: up})
	if err == nil {
		t.Fatal("error = nil")
	}
	if len(up.dbBodies) != 1 {
		t.Errorf("%d appel(s) Update, want 1 : aucun enchaînement après une issue inconnue", len(up.dbBodies))
	}
	msg := err.Error()
	for _, want := range []string{"issue inconnue", "notion-seed plan", "  → "} {
		if !strings.Contains(msg, want) {
			t.Errorf("erreur = %q, want contenant %q", msg, want)
		}
	}
	if strings.Contains(msg, "import") {
		t.Errorf("erreur = %q, want sans import : il n'y a rien à ré-adopter", msg)
	}
}

// overlayFixture : une entrée de state antérieure qui connaît une propriété
// hors config (Hors), et une cible qui renomme la database et ajoute une option
// à Prio.
func overlayFixture() (prior state.Database, target *state.Database, p *diff.Plan) {
	prior = state.Database{
		ID: "db-1", DataSourceID: "ds-1", Name: "Avant",
		Properties: map[string]state.Property{
			"Prio": {ID: "p1", Type: "select"},
			"Hors": {ID: "h1", Type: "number"},
		},
	}
	target = &state.Database{
		ID: "db-1", DataSourceID: "ds-1", Name: "Tasks",
		Properties: map[string]state.Property{
			"Prio":  {ID: "p1", Type: "select", Options: []state.Option{{Key: "haute", Name: "Haute"}}},
			"Notes": {ID: "n1", Type: "rich_text"},
		},
	}
	p = &diff.Plan{Changes: []diff.Change{updateChange("tasks", target, nameDetail, prioDetail)}}
	return prior, target, p
}

// Écrite mais pas relue : le state porte ce qui a été ÉCRIT, superposé à
// l'entrée d'avant. Garder l'entrée d'avant ferait passer notre propre écriture
// pour une dérive venue d'ailleurs au prochain plan.
func TestRunRecordsWhatWasWrittenWhenTheReReadFails(t *testing.T) {
	dir := t.TempDir()
	prior, _, p := overlayFixture()
	up := &fakeUpdater{readErr: errors.New("relecture impossible")}
	snap := emptySnapshot()
	snap.Databases["tasks"] = prior

	_, err := Run(context.Background(), p, snap, Options{Dir: dir, Updater: up})
	if err == nil {
		t.Fatal("error = nil")
	}
	if !strings.Contains(err.Error(), "relecture impossible") || !strings.Contains(err.Error(), "  → ") {
		t.Errorf("erreur = %q", err)
	}

	loaded, lerr := state.Load(dir)
	if lerr != nil {
		t.Fatal(lerr)
	}
	got := loaded.Databases["tasks"]
	if got.Name != "Tasks" {
		t.Errorf("Name = %q, want Tasks (écrit)", got.Name)
	}
	if opts := got.Properties["Prio"].Options; len(opts) != 1 || opts[0].Key != "haute" {
		t.Errorf("Prio.Options = %v, want l'option écrite, key comprise", opts)
	}
	if got.Properties["Hors"].ID != "h1" {
		t.Error("Hors, connue du state mais hors du jeu d'écriture, a été perdue")
	}
	if _, ok := got.Properties["Notes"]; ok {
		t.Error("Notes n'a pas été écrite : elle ne doit pas entrer dans le state")
	}
}

// Second PATCH en échec : seul le nom est passé. Le state le porte, et garde
// les propriétés d'avant, que rien n'a touchées.
func TestRunRecordsTheDatabaseFieldsWhenTheSecondPatchFails(t *testing.T) {
	dir := t.TempDir()
	prior, _, p := overlayFixture()
	up := &fakeUpdater{err: &transport.APIError{Status: 400, NotionCode: "validation_error", Message: "boom"}}
	snap := emptySnapshot()
	snap.Databases["tasks"] = prior

	if _, err := Run(context.Background(), p, snap, Options{Dir: dir, Updater: up}); err == nil {
		t.Fatal("error = nil")
	}

	loaded, lerr := state.Load(dir)
	if lerr != nil {
		t.Fatal(lerr)
	}
	got := loaded.Databases["tasks"]
	if got.Name != "Tasks" {
		t.Errorf("Name = %q, want Tasks (le PATCH database est passé)", got.Name)
	}
	if opts := got.Properties["Prio"].Options; len(opts) != 0 {
		t.Errorf("Prio.Options = %v, want l'état d'avant : le data source n'a pas été écrit", opts)
	}
	if got.Properties["Hors"].ID != "h1" {
		t.Error("Hors a été perdue")
	}
}

// Premier PATCH en échec : rien n'est passé, rien n'est inscrit.
func TestRunLeavesStateUntouchedWhenTheFirstPatchFails(t *testing.T) {
	dir := t.TempDir()
	prior, _, p := overlayFixture()
	up := &fakeUpdater{err: &transport.APIError{Status: 400, NotionCode: "validation_error", Message: "boom"}, dbFails: true}
	snap := emptySnapshot()
	snap.Databases["tasks"] = prior

	if _, err := Run(context.Background(), p, snap, Options{Dir: dir, Updater: up}); err == nil {
		t.Fatal("error = nil")
	}
	if snap.Databases["tasks"].Name != "Avant" {
		t.Errorf("Name = %q, want Avant : rien n'a été écrit", snap.Databases["tasks"].Name)
	}
}

// fakeTrasher enregistre les ids qu'on lui demande de mettre à la corbeille.
// before, s'il est posé, tourne au début de chaque appel : c'est ce qui permet
// de vérifier que le state est déjà sur disque au moment de l'appel suivant.
type fakeTrasher struct {
	ids       []string
	confirmed bool
	err       error
	failAt    int
	before    func(call int)
}

func (f *fakeTrasher) Trash(_ context.Context, id string) (bool, error) {
	call := len(f.ids)
	if f.before != nil {
		f.before(call)
	}
	f.ids = append(f.ids, id)
	if f.err != nil && call == f.failAt {
		return false, f.err
	}
	return f.confirmed, nil
}

func destroyChange(key string) diff.Change {
	return diff.Change{
		Resource: "database." + key, Key: key,
		Kind: resources.KindDestroy, Class: diff.ClassDestructive,
		Details: []resources.Detail{
			resources.NewDetail("-", "database."+key, diff.ClassDestructive),
		},
	}
}

// orphanSnapshot ancre chaque key avec l'id "db-<key>" : un test peut ainsi dire
// quel id a été mis à la corbeille.
func orphanSnapshot(keys ...string) *state.Snapshot {
	snap := emptySnapshot()
	for _, k := range keys {
		snap.Databases[k] = state.Database{ID: "db-" + k, DataSourceID: "ds-" + k, Name: k}
	}
	return snap
}

// L'identité mise à la corbeille est celle du state, et le state est sauvé
// après CHAQUE destruction : une interruption laisse un fichier exactement vrai.
func TestRunTrashesEachOrphanFromItsStateIdentityAndSavesAfterEach(t *testing.T) {
	dir := t.TempDir()
	snap := orphanSnapshot("archive", "tasks")
	// Le state est sur disque avant le premier appel, comme en vrai : sans ça,
	// la relecture ci-dessous ne prouverait rien.
	if err := state.Save(dir, snap); err != nil {
		t.Fatal(err)
	}
	var goneBeforeSecond bool
	tr := &fakeTrasher{confirmed: true, before: func(call int) {
		if call != 1 {
			return
		}
		loaded, err := state.Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		_, still := loaded.Databases["archive"]
		goneBeforeSecond = !still
	}}
	p := &diff.Plan{Changes: []diff.Change{destroyChange("archive"), destroyChange("tasks")}}

	rep, err := Run(context.Background(), p, snap, Options{Dir: dir, Trasher: tr})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if want := []string{"db-archive", "db-tasks"}; !slices.Equal(tr.ids, want) {
		t.Errorf("ids mis à la corbeille = %v, want %v", tr.ids, want)
	}
	if !goneBeforeSecond {
		t.Error("l'entrée de database.archive était encore sur disque avant la deuxième destruction")
	}
	if len(rep.Destroyed) != 2 || !strings.HasPrefix(rep.Destroyed[0], "database.archive mise à la corbeille") {
		t.Errorf("Destroyed = %v", rep.Destroyed)
	}
	loaded, lerr := state.Load(dir)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if len(loaded.Databases) != 0 {
		t.Errorf("state = %v, want vide", loaded.Databases)
	}
	if !rep.Converged() {
		t.Errorf("Converged() = false : %+v", rep)
	}
}

// Review Focus #1 : un 200 qui ne confirme pas la corbeille garde l'entrée.
// L'abandonner rendrait invisible une database encore vivante : ni déclarée, ni
// dans le state.
func TestRunKeepsTheStateEntryWhenTheAPIDoesNotConfirmTheTrash(t *testing.T) {
	dir := t.TempDir()
	snap := orphanSnapshot("tasks")
	p := &diff.Plan{Changes: []diff.Change{destroyChange("tasks")}}

	rep, err := Run(context.Background(), p, snap, Options{Dir: dir, Trasher: &fakeTrasher{}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(rep.Destroyed) != 0 {
		t.Errorf("Destroyed = %v, want vide", rep.Destroyed)
	}
	if len(rep.Mismatches) != 1 || !strings.Contains(rep.Mismatches[0], "database.tasks") ||
		!strings.Contains(rep.Mismatches[0], "gardée") {
		t.Errorf("Mismatches = %v, want un écart nommant database.tasks et l'entrée gardée", rep.Mismatches)
	}
	if _, ok := snap.Databases["tasks"]; !ok {
		t.Error("l'entrée a été retirée alors que la corbeille n'est pas confirmée")
	}
	if _, serr := os.Stat(state.Path(dir)); !os.IsNotExist(serr) {
		t.Error("un state a été écrit alors que rien n'a été détruit")
	}
	if rep.Converged() {
		t.Error("Converged() = true alors que la database n'est pas à la corbeille")
	}
}

// Une corbeille CONFIRMÉE dont l'écriture du state échoue ensuite ne doit
// jamais laisser croire que rien ne s'est passé : la database est déjà à la
// corbeille dans Notion, seul l'enregistrement local a échoué.
func TestRunWarnsWhenTrashSucceedsButStateSaveFails(t *testing.T) {
	dir := t.TempDir()
	snap := orphanSnapshot("tasks")
	// Le state est sur disque avant l'appel, comme en vrai : sans ça, la
	// relecture ci-dessous ne prouverait rien.
	if err := state.Save(dir, snap); err != nil {
		t.Fatal(err)
	}

	// Un dossier non inscriptible fait échouer state.Save après une corbeille
	// déjà confirmée par l'API.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skipf("chmod indisponible ici: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	p := &diff.Plan{Changes: []diff.Change{destroyChange("tasks")}}
	_, err := Run(context.Background(), p, snap, Options{
		Dir: dir, Trasher: &fakeTrasher{confirmed: true},
	})
	if err == nil {
		_ = os.Chmod(dir, 0o700)
		t.Skip("le dossier reste inscriptible (root ?), test non significatif")
	}

	for _, want := range []string{
		"database.tasks", "corbeille", "  → ", "notion-seed plan",
		"entrée de state obsolète", "`apply` suivant retirera",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("erreur = %q, want contenant %q", err.Error(), want)
		}
	}

	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	loaded, lerr := state.Load(dir)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if _, still := loaded.Databases["tasks"]; !still {
		t.Error("le state sur disque a perdu l'entrée malgré l'échec d'écriture")
	}
}

// Review Focus #2 et D-B1 : aucun échec ne retire l'entrée. Le 404 en
// particulier ne vaut pas destruction — il renvoie au plan, qui relit le réel,
// et n'accuse jamais le partage.
func TestRunKeepsTheStateEntryWhenTrashingFails(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		want     []string
		unwanted []string
	}{
		{
			name: "404",
			err: &transport.APIError{Status: 404, NotionCode: "object_not_found",
				Message: "Could not find database with ID: db-tasks."},
			want:     []string{"404", "notion-seed plan", "entrée de state obsolète"},
			unwanted: []string{"partagée", "intégration"},
		},
		{
			name: "ancêtre archivé",
			err: &transport.APIError{Status: 400, NotionCode: "validation_error",
				Message: "Can't edit page on block with an archived ancestor. You must " +
					"unarchive the ancestor before editing page."},
			want: []string{"page ancêtre", "restaurez la page parente", "relancez apply",
				"supprimez définitivement la page parente", "notion-seed plan",
				"entrée de state obsolète", "`apply` suivant retirera"},
		},
		{
			name: "issue inconnue",
			err:  &transport.OutcomeUnknownError{Cause: context.DeadlineExceeded},
			want: []string{"issue inconnue", "notion-seed plan", "entrée de state obsolète"},
		},
		{
			name: "refus générique",
			err: &transport.APIError{Status: 409, NotionCode: "conflict_error",
				Message: "Conflict occurred while saving."},
			want: []string{"impossible", "relancez apply"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			snap := orphanSnapshot("tasks")
			p := &diff.Plan{Changes: []diff.Change{destroyChange("tasks")}}

			_, err := Run(context.Background(), p, snap, Options{
				Dir: dir, Trasher: &fakeTrasher{err: tc.err},
			})
			if err == nil {
				t.Fatal("Run() error = nil, want l'échec de la corbeille")
			}
			// Le rappel de ce qui est acquis suit un point-virgule : après un
			// point, il commencerait par une minuscule.
			wants := append([]string{"database.tasks", "entrée de state est gardée", "  → ",
				" ; aucune écriture n'avait abouti avant celle-ci"}, tc.want...)
			for _, want := range wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message = %q, il doit contenir %q", err.Error(), want)
				}
			}
			for _, unwanted := range tc.unwanted {
				if strings.Contains(err.Error(), unwanted) {
					t.Errorf("message = %q, il ne doit pas contenir %q", err.Error(), unwanted)
				}
			}
			if _, ok := snap.Databases["tasks"]; !ok {
				t.Error("l'entrée a été retirée malgré l'échec")
			}
			if _, serr := os.Stat(state.Path(dir)); !os.IsNotExist(serr) {
				t.Error("un state a été écrit malgré l'échec")
			}
		})
	}
}

// Un échec au milieu de la série nomme ce qui est acquis avant lui : sans ça,
// l'utilisateur ne sait pas quelles databases sont déjà à la corbeille.
func TestRunNamesTheDestroysAcquiredBeforeAFailure(t *testing.T) {
	dir := t.TempDir()
	snap := orphanSnapshot("archive", "tasks")
	tr := &fakeTrasher{confirmed: true, failAt: 1, err: &transport.APIError{
		Status: 409, NotionCode: "conflict_error", Message: "Conflict occurred while saving."}}
	p := &diff.Plan{Changes: []diff.Change{destroyChange("archive"), destroyChange("tasks")}}

	_, err := Run(context.Background(), p, snap, Options{Dir: dir, Trasher: tr})
	if err == nil {
		t.Fatal("Run() error = nil, want l'échec de la seconde destruction")
	}
	if want := "déjà mises à la corbeille et retirées du state : database.archive"; !strings.Contains(err.Error(), want) {
		t.Errorf("message = %q, il doit contenir %q", err.Error(), want)
	}
	loaded, lerr := state.Load(dir)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if _, ok := loaded.Databases["archive"]; ok {
		t.Error("database.archive est encore dans le state alors qu'elle est à la corbeille")
	}
	if _, ok := loaded.Databases["tasks"]; !ok {
		t.Error("database.tasks a quitté le state alors que sa destruction a échoué")
	}
}

// Review Focus #4 : prevent_destroy est un accusé de lecture. Run ne le lit
// pas, et la destruction part.
func TestRunTrashesADestroyAcknowledgedByPreventDestroy(t *testing.T) {
	c := destroyChange("tasks")
	c.Acknowledged = []string{"prevent_destroy"}
	tr := &fakeTrasher{confirmed: true}

	rep, err := Run(context.Background(), &diff.Plan{Changes: []diff.Change{c}},
		orphanSnapshot("tasks"), Options{Dir: t.TempDir(), Trasher: tr})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(tr.ids) != 1 || len(rep.Destroyed) != 1 {
		t.Errorf("ids = %v, Destroyed = %v : prevent_destroy ne doit rien empêcher", tr.ids, rep.Destroyed)
	}
}

func TestRunRefusesADestroyWithoutATrasher(t *testing.T) {
	_, err := Run(context.Background(), &diff.Plan{Changes: []diff.Change{destroyChange("tasks")}},
		orphanSnapshot("tasks"), Options{Dir: t.TempDir()})
	if err == nil {
		t.Fatal("error = nil, want un défaut interne nommé")
	}
	for _, want := range []string{"database.tasks", "défaut interne", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("erreur = %q, want contenant %q", err, want)
		}
	}
}

// L'absence de Trasher se voit AVANT la première écriture : sinon un plan qui
// crée puis détruit écrirait la création, puis s'arrêterait sur la
// destruction, à moitié appliqué.
func TestRunRefusesAMissingWriterBeforeAnyWrite(t *testing.T) {
	dir := t.TempDir()
	snap := orphanSnapshot("tasks")
	p := &diff.Plan{Changes: []diff.Change{
		createChange("projects", "Projects"),
		destroyChange("tasks"),
	}}
	rep, err := Run(context.Background(), p, snap, Options{Dir: dir, Creator: refuseToCreate(t)})
	if err == nil {
		t.Fatal("error = nil, want un défaut interne nommé")
	}
	for _, want := range []string{"database.tasks", "défaut interne", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("erreur = %q, want contenant %q", err, want)
		}
	}
	if len(rep.Created) != 0 {
		t.Errorf("Created = %v, want aucune création avant le refus", rep.Created)
	}
}
