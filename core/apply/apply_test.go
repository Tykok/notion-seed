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

// refuseToCreate serves the tests where no creation must be attempted.
func refuseToCreate(t *testing.T) creatorFunc {
	t.Helper()
	return func(context.Context, []byte) (resources.CreatedDatabase, error) {
		t.Error("no creation was to be attempted")
		return resources.CreatedDatabase{}, errors.New("creation forbidden in this test")
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

// The state must be written AFTER EACH creation, not once at the end: a stop
// midway then leaves an exactly true state, and no database created without an
// anchor.
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
			// At the SECOND call, the first one must already be on disk.
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
		t.Error("the state was not on disk before the second creation")
	}
	if len(rep.Created) != 2 {
		t.Errorf("Created = %v, want 2 entries", rep.Created)
	}
	if !rep.Converged() {
		t.Errorf("Converged() = false, want true: %+v", rep)
	}
}

// A failure on the second POST leaves the first creation in the state, and the
// message must name both — what is done, and what is not.
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
		t.Fatal("Run() error = nil, want the failure of the second creation")
	}
	for _, want := range []string{"database.tasks", "database.projects", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, it must contain %q", err.Error(), want)
		}
	}
	loaded, lerr := state.Load(dir)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if _, ok := loaded.Databases["projects"]; !ok {
		t.Error("database.projects must stay in the state: it really exists")
	}
	if _, ok := loaded.Databases["tasks"]; ok {
		t.Error("database.tasks must not be in the state: it was not created")
	}
}

// An unknown outcome is a hard stop: we don't know whether the mutation was
// applied, so chaining would work on an undetermined workspace.
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
		t.Fatal("Run() error = nil, want the unknown outcome")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1: nothing is chained after an unknown outcome", calls)
	}
	for _, want := range []string{"database.projects", "Projects", "import", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, it must contain %q", err.Error(), want)
		}
	}
}

// The identity survives a failed read-back: losing it would cost a duplicate.
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
		t.Fatal("Run() error = nil, want the failed read-back reported")
	}
	if !strings.Contains(err.Error(), "import") {
		t.Errorf("message = %q, it must point to resyncing by import", err.Error())
	}
	loaded, lerr := state.Load(dir)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if got := loaded.Databases["projects"]; got.ID != "db-1" || got.DataSourceID != "ds-1" {
		t.Errorf("state = %+v, want the db-1/ds-1 identity kept", got)
	}
}

// A blocked plan must NEVER be written, and the guard must live in the package
// that writes — not only in the command. A plan can be blocked by another
// resource than the ones it is about to create: without this guard, a second
// caller would write the creations of a rejected plan.
func TestRunRefusesABlockedPlan(t *testing.T) {
	dir := t.TempDir()
	p := &diff.Plan{
		Changes:        []diff.Change{createChange("projects", "Projects")},
		Blocked:        true,
		BlockedReasons: []string{"database.tasks: silent rewrite"},
	}

	_, err := Run(context.Background(), p, emptySnapshot(), Options{
		Dir: dir, ParentPageID: testParentPageID, Creator: refuseToCreate(t),
	})
	if err == nil {
		t.Fatal("Run() error = nil, want a blocked plan rejected")
	}
	if !strings.Contains(err.Error(), "  → ") {
		t.Errorf("message = %q, it must carry a corrective action", err.Error())
	}
	if _, serr := os.Stat(state.Path(dir)); !os.IsNotExist(serr) {
		t.Error("a state was written despite a blocked plan")
	}
}

// The unknown-outcome message must name the parent page: the user will go and
// check there, at the precise moment their workspace is undetermined. Looking
// the id up in workspace.yaml is work the command can do.
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
		t.Fatal("Run() error = nil, want the unknown outcome")
	}
	if !strings.Contains(err.Error(), testParentPageID) {
		t.Errorf("message = %q, it must name the parent page %s", err.Error(), testParentPageID)
	}
}

// An option the API wrote IN EXCESS must read as such. Reusing the plan's note
// as is gave "the API did not write - option "Fait" — absent from the YAML",
// which describes the opposite of what happened.
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

	// The API returns one more option than what was announced.
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
		t.Fatalf("Mismatches = %v, it must name the extra option", rep.Mismatches)
	}
	if strings.Contains(joined, "did not write") {
		t.Errorf("mismatch = %q: an option written in excess cannot read "+
			"\"the API did not write\"", joined)
	}
	if !strings.Contains(joined, "in excess") {
		t.Errorf("mismatch = %q, it must say the option is in excess", joined)
	}
}

// A stale state entry is removed: it writes nothing to Notion and makes the
// plan converge.
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
		t.Errorf("Cleaned = %v, want 1 entry", rep.Cleaned)
	}
	loaded, lerr := state.Load(dir)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if _, ok := loaded.Databases["tasks"]; ok {
		t.Error("the stale entry was not removed")
	}
}

// Review Focus #3: an empty Withheld IS the permission to write. An allowed
// update without a target is a notion-seed bug: skipping it would make an
// apply converge that did not write what the plan showed, and stopping on it
// would leave written the creation before it. The whole plan is rejected,
// before the first call.
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
		t.Fatal("Run() error = nil, want an allowed change without a target rejected")
	}
	for _, want := range []string{"database.tasks", "notion-seed bug", "nothing was applied", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, it must contain %q", err.Error(), want)
		}
	}
	if _, serr := os.Stat(state.Path(dir)); !os.IsNotExist(serr) {
		t.Error("a state was written while the plan is rejected")
	}
}

// A destruction has no target: its identity comes from the state. Without an
// id, the PATCH would target "/v1/databases/", which points at nothing.
func TestCheckRefusesADestroyWithoutAnIdentityInTheState(t *testing.T) {
	p := &diff.Plan{Changes: []diff.Change{
		{Resource: "database.tasks", Key: "tasks", Kind: resources.KindDestroy},
	}}
	snap := emptySnapshot()
	snap.Databases["tasks"] = state.Database{Name: "Tasks"}

	err := Check(p, snap)
	if err == nil {
		t.Fatal("Check() error = nil, want a rejection")
	}
	for _, want := range []string{"database.tasks", "notion-seed bug", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, it must contain %q", err.Error(), want)
		}
	}

	snap.Databases["tasks"] = state.Database{ID: "db-1", Name: "Tasks"}
	if err := Check(p, snap); err != nil {
		t.Errorf("Check() error = %v, want nil: the identity is in the state", err)
	}
}

// A withheld resource has no target, and on purpose: Check lets it through,
// Run will name it without writing it.
func TestCheckLetsAWithheldChangeThrough(t *testing.T) {
	c := updateChange("tasks", nil, prioDetail)
	c.Withheld = "an option must be migrated by hand"
	if err := Check(&diff.Plan{Changes: []diff.Change{c}}, emptySnapshot()); err != nil {
		t.Errorf("Check() error = %v, want nil", err)
	}
}

// The API did not write what was announced: the creation stays done, the
// state stays true, but the mismatch is reported. It is the product's thesis
// applied to notion-seed's own write.
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

	// The API returns a database WITHOUT the Budget property.
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
		t.Errorf("Created = %v, want the creation done", rep.Created)
	}
	joined := strings.Join(rep.Mismatches, "\n")
	if !strings.Contains(joined, "Budget") {
		t.Errorf("Mismatches = %v, it must name Budget", rep.Mismatches)
	}
	if rep.Converged() {
		t.Error("Converged() = true despite a mismatch between the target and the actual state")
	}
}

// A matching creation must produce NO mismatch: without this test, an overly
// chatty comparator would fail every successful apply.
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
		t.Errorf("Mismatches = %v, want empty", rep.Mismatches)
	}
}

// The payload comes from the TARGET, not from the configuration: it is the
// invariant everything else protects. It is checked on what the creator really
// receives.
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
	// The declared group goes out as is, and above all: no "To-do" substituted.
	if !strings.Contains(sent, `"group":"In progress"`) {
		t.Errorf("payload = %s, it must carry the declared group", sent)
	}
	if strings.Contains(sent, `"To-do"`) {
		t.Errorf("payload = %s, no group must be substituted", sent)
	}
	if !strings.Contains(sent, testParentPageID) {
		t.Errorf("payload = %s, it must target the parent page", sent)
	}
}

// fakeUpdater records what it is asked to write.
//
// err happens at the call of index failAt (the previous ones succeed). dbFails
// says whether the failure hits the database PATCH — otherwise, it is the data
// source PATCH that fails, and the database is written if a body was meant for
// it.
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
		t.Fatalf("Updated = %v, want 1 line", rep.Updated)
	}
	body := string(up.dsBodies[0])
	if !strings.Contains(body, "Prio") {
		t.Errorf("payload = %s, want Prio", body)
	}
	if strings.Contains(body, "Notes") {
		t.Errorf("payload = %s, want without Notes: the plan does not show it", body)
	}
	if up.dbBodies[0] != nil {
		t.Errorf("dbBody = %s, want nil: no database field in the plan", up.dbBodies[0])
	}
	if len(rep.Mismatches) != 0 {
		t.Errorf("Mismatches = %v, want empty", rep.Mismatches)
	}

	// The state is saved from the READ-BACK: the property id exists only there.
	loaded, lerr := state.Load(dir)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if got := loaded.Databases["tasks"].Properties["Prio"].ID; got != "p1" {
		t.Errorf("state Prio.ID = %q, want p1 (read back)", got)
	}
}

// Review Focus #3: an update that touches only a field does not call the data
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
		t.Fatalf("%d Update call(s), want 1", len(up.dbBodies))
	}
	if up.dsBodies[0] != nil {
		t.Errorf("dsBody = %s, want nil", up.dsBodies[0])
	}
	if !strings.Contains(string(up.dbBodies[0]), "Tâches") {
		t.Errorf("dbBody = %s, want the new title", up.dbBodies[0])
	}
}

// The "plan ≡ payload" equivalence goes both ways. The previous test covers
// inclusion: nothing goes out that is not in the plan. This one covers the
// other: nothing of the plan stays on the ground.
func TestWriteSetCoversEveryDetail(t *testing.T) {
	details := []resources.Detail{
		{Op: "~", Target: "name", Field: "name"},
		{Op: "~", Target: "icon", Field: "icon"},
		{Op: "~", Target: `property "Prio"`, Property: "Prio"},
		{Op: "-", Target: `option "Basse" (property "Prio")`, Property: "Prio"},
		{Op: "+", Target: `property "Neuve"`, Property: "Neuve"},
	}
	fields, props := writeSet(details)

	for _, d := range details {
		switch {
		case d.Field != "":
			if !slices.Contains(fields, d.Field) {
				t.Errorf("plan field %q missing from the write set", d.Field)
			}
		case d.Property != "":
			if !slices.Contains(props, d.Property) {
				t.Errorf("plan property %q missing from the write set", d.Property)
			}
		default:
			t.Errorf("detail %q without Field or Property: it cannot be written", d.Target)
		}
	}
	// Deduplicated: "Prio" carries two lines, a single write.
	if len(props) != 2 {
		t.Errorf("props = %v, want 2 (Prio deduplicated, Neuve)", props)
	}
}

// The option lines under a new or retyped property say what goes out; they add
// NO write: the write set is that of the property lines alone.
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
		t.Fatalf("%d option lines, want 3:\n%v", optionLines, res.Changeset.Details)
	}
	_, got := writeSet(res.Changeset.Details)
	_, want := writeSet(withoutOptions)
	if !slices.Equal(got, want) {
		t.Errorf("write set = %v, want %v: the option lines write nothing more", got, want)
	}
	if !slices.Equal(got, []string{"Etat", "Prio"}) {
		t.Errorf("write set = %v, want [Etat Prio]", got)
	}
}

// The first trap of §1: the API replaces the whole list of options, and an
// existing option sent without its id is destroyed then re-created. Each piece
// is covered alone; this test locks their COMPOSITION — from the plan to the
// body sent —, which a second target builder or a copy of Target losing the id
// would break without any unit test moving.
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
	// "Haute" stays, "Moyenne" arrives, "Basse" is no longer claimed.
	desired := state.Database{Properties: map[string]state.Property{
		"Name": {Type: "title"},
		"Prio": {Type: "select", Options: []state.Option{
			{Key: "haute", Name: "Haute", Color: "red"},
			{Key: "moyenne", Name: "Moyenne", Color: "orange"},
		}},
	}}
	res := diff.CompareDatabase("tasks", &desired, &applied, &actual)
	if res.Withheld != "" || res.Target == nil {
		t.Fatalf("setup is wrong: Withheld=%q Target=%v", res.Withheld, res.Target)
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
		t.Fatalf("dsBodies = %v, want a data source PATCH", up.dsBodies)
	}

	var body struct {
		Properties map[string]struct {
			Select struct {
				Options []map[string]any `json:"options"`
			} `json:"select"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(up.dsBodies[0], &body); err != nil {
		t.Fatalf("unreadable body %s: %v", up.dsBodies[0], err)
	}
	if _, sent := body.Properties["Name"]; sent {
		t.Errorf("payload = %s: Name is not in the plan", up.dsBodies[0])
	}
	byName := map[string]map[string]any{}
	for _, o := range body.Properties["Prio"].Select.Options {
		byName[o["name"].(string)] = o
	}
	if got := byName["Haute"]["id"]; got != "o-haute" {
		t.Errorf("Haute goes out with id %v, want o-haute: without it, the API re-creates it", got)
	}
	moyenne, ok := byName["Moyenne"]
	if !ok {
		t.Fatalf("payload = %s: the new option Moyenne is missing", up.dsBodies[0])
	}
	if id, has := moyenne["id"]; has {
		t.Errorf("Moyenne goes out with id %v, want none: it is new", id)
	}
	if _, sent := byName["Basse"]; sent {
		t.Errorf("payload = %s: Basse is not claimed, it must not go out", up.dsBodies[0])
	}
	if len(byName) != 2 {
		t.Errorf("options sent = %v, want exactly Haute and Moyenne", byName)
	}
}

func TestRunSkipsAWithheldResourceWithoutCallingTheAPI(t *testing.T) {
	dir := t.TempDir()
	up := &fakeUpdater{}
	c := updateChange("tasks", nil, prioDetail)
	c.Withheld = "an option must be migrated by hand"
	p := &diff.Plan{Changes: []diff.Change{c}}

	rep, err := Run(context.Background(), p, emptySnapshot(), Options{Dir: dir, Updater: up})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if up.calls() != 0 {
		t.Error("a call took place on a withheld resource")
	}
	if len(rep.Skipped) != 1 || rep.Skipped[0] != "database.tasks" {
		t.Errorf("Skipped = %v, want [database.tasks]", rep.Skipped)
	}
}

// Review Focus #2: without a data_source_id, the PATCH would target
// "/v1/data_sources/", which points at nothing. The target always comes from a
// fresh read-back: an empty id is a notion-seed bug, rejected before any call.
func TestRunRefusesAnUpdateWithoutADataSourceID(t *testing.T) {
	dir := t.TempDir()
	target := prioTarget()
	target.DataSourceID = ""
	up := &fakeUpdater{}
	p := &diff.Plan{Changes: []diff.Change{updateChange("tasks", target, prioDetail)}}

	_, err := Run(context.Background(), p, emptySnapshot(), Options{Dir: dir, Updater: up})
	if err == nil {
		t.Fatal("error = nil, want a rejection before any call")
	}
	if up.calls() != 0 {
		t.Error("a call took place despite the missing data_source_id")
	}
	for _, want := range []string{"database.tasks", "notion-seed bug", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want containing %q", err, want)
		}
	}
}

// The CLI wires the Updater only in the next task: an update without an
// Updater must be a named bug, not a nil dereference.
func TestRunRefusesAnUpdateWithoutAnUpdater(t *testing.T) {
	p := &diff.Plan{Changes: []diff.Change{updateChange("tasks", prioTarget(), prioDetail)}}

	_, err := Run(context.Background(), p, emptySnapshot(), Options{Dir: t.TempDir()})
	if err == nil {
		t.Fatal("error = nil, want a named notion-seed bug")
	}
	for _, want := range []string{"database.tasks", "notion-seed bug", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want containing %q", err, want)
		}
	}
}

// The 404 of the data source PATCH blames the sharing with the integration,
// while the cause can be an archived ancestor. Probing the database decides.
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
		t.Fatal("error = nil, want a diagnosed error")
	}
	if up.probes != 1 {
		t.Errorf("%d probe(s), want 1", up.probes)
	}
	for _, want := range []string{"ancestor", "trash", "restore the parent page", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want containing %q", err, want)
		}
	}
	for _, unwanted := range []string{"shared", "integration"} {
		if strings.Contains(err.Error(), unwanted) {
			t.Errorf("error = %q, want WITHOUT the sharing advice, which is wrong here", err)
		}
	}
}

// An ancestor in the trash fails the database PATCH FIRST. When that PATCH went
// through, a 404 from the data source therefore cannot come from an archived
// ancestor, even if the database can still be read: advising to restore the
// parent page would send the user looking for a cause that does not exist.
func TestRunDoesNotBlameAnArchivedAncestorAfterADatabaseWrite(t *testing.T) {
	up := &fakeUpdater{
		err: &transport.APIError{Status: 404, NotionCode: "object_not_found",
			Message: "Could not find data_source"},
		exists: true,
	}
	p := &diff.Plan{Changes: []diff.Change{updateChange("tasks", prioTarget(), nameDetail, prioDetail)}}

	_, err := Run(context.Background(), p, emptySnapshot(), Options{Dir: t.TempDir(), Updater: up})
	if err == nil {
		t.Fatal("error = nil, want a diagnosed error")
	}
	if len(up.dbBodies) != 1 || up.dbBodies[0] == nil {
		t.Fatalf("setup is wrong: the database PATCH was supposed to go out")
	}
	for _, unwanted := range []string{"ancestor", "trash", "restore the parent page"} {
		if strings.Contains(err.Error(), unwanted) {
			t.Errorf("error = %q, want WITHOUT %q: the successful database PATCH rules it out", err, unwanted)
		}
	}
	for _, want := range []string{"data source", "shared", "already written on this resource: the name", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want containing %q", err, want)
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
		t.Fatal("error = nil, want a diagnosed error")
	}
	for _, want := range []string{"vanished", "shared with the integration", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want containing %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "trash") {
		t.Errorf("error = %q, want WITHOUT the archived-ancestor diagnosis", err)
	}
}

// If the probe itself fails, nothing is decided — but the 404's sharing advice,
// which can be wrong, is not relayed either.
func TestRunFallsBackWhenTheProbeFails(t *testing.T) {
	up := &fakeUpdater{
		err: &transport.APIError{Status: 404, NotionCode: "object_not_found",
			Message: "Make sure the relevant pages are shared with your integration"},
		existsErr: errors.New("network down"),
	}
	p := &diff.Plan{Changes: []diff.Change{updateChange("tasks", prioTarget(), prioDetail)}}

	_, err := Run(context.Background(), p, emptySnapshot(), Options{Dir: t.TempDir(), Updater: up})
	if err == nil {
		t.Fatal("error = nil")
	}
	for _, want := range []string{"network down", "404", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want containing %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "shared with your integration") {
		t.Errorf("error = %q, want without the raw text of the 404", err)
	}
}

// Measured: under an archived ancestor, the database PATCH fails first, with a
// 400 that names the cause. The message states the fix.
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
	for _, want := range []string{"restore the parent page", "nothing was written on this resource"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want containing %q", err, want)
		}
	}
}

// Failure of the second PATCH: the message names EXACTLY the fields written,
// says no data is touched, and lists what was done before.
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
		t.Fatal("error = nil, want the failure of the second PATCH")
	}
	if len(rep.Updated) != 1 {
		t.Errorf("Updated = %v, want the first update done", rep.Updated)
	}
	msg := err.Error()
	for _, want := range []string{"database.projects", "the name", "no row data", "database.tasks", "  → "} {
		if !strings.Contains(msg, want) {
			t.Errorf("error = %q, want containing %q", msg, want)
		}
	}
	for _, unwanted := range []string{"icon", "description"} {
		if strings.Contains(msg, unwanted) {
			t.Errorf("error = %q: %q was not sent, it must not be named", msg, unwanted)
		}
	}
}

// Unknown outcome: hard stop, nothing chained. `plan` is enough to see the
// actual state — unlike creation, there is no identity to re-adopt.
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
		t.Errorf("%d Update call(s), want 1: nothing chained after an unknown outcome", len(up.dbBodies))
	}
	msg := err.Error()
	for _, want := range []string{"unknown outcome", "notion-seed plan", "  → "} {
		if !strings.Contains(msg, want) {
			t.Errorf("error = %q, want containing %q", msg, want)
		}
	}
	if strings.Contains(msg, "import") {
		t.Errorf("error = %q, want without import: there is nothing to re-adopt", msg)
	}
}

// overlayFixture: a prior state entry that knows an unmanaged property (Hors),
// and a target that renames the database and adds an option to Prio.
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

// Written but not read back: the state holds what was WRITTEN, laid over the
// previous entry. Keeping the previous entry would pass notion-seed's own
// write off as drift from elsewhere in the next plan.
func TestRunRecordsWhatWasWrittenWhenTheReReadFails(t *testing.T) {
	dir := t.TempDir()
	prior, _, p := overlayFixture()
	up := &fakeUpdater{readErr: errors.New("read-back failed")}
	snap := emptySnapshot()
	snap.Databases["tasks"] = prior

	_, err := Run(context.Background(), p, snap, Options{Dir: dir, Updater: up})
	if err == nil {
		t.Fatal("error = nil")
	}
	if !strings.Contains(err.Error(), "read-back failed") || !strings.Contains(err.Error(), "  → ") {
		t.Errorf("error = %q", err)
	}

	loaded, lerr := state.Load(dir)
	if lerr != nil {
		t.Fatal(lerr)
	}
	got := loaded.Databases["tasks"]
	if got.Name != "Tasks" {
		t.Errorf("Name = %q, want Tasks (written)", got.Name)
	}
	if opts := got.Properties["Prio"].Options; len(opts) != 1 || opts[0].Key != "haute" {
		t.Errorf("Prio.Options = %v, want the written option, key included", opts)
	}
	if got.Properties["Hors"].ID != "h1" {
		t.Error("Hors, known to the state but outside the write set, was lost")
	}
	if _, ok := got.Properties["Notes"]; ok {
		t.Error("Notes was not written: it must not enter the state")
	}
}

// Second PATCH failing: only the name went through. The state holds it, and
// keeps the previous properties, which nothing touched.
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
		t.Errorf("Name = %q, want Tasks (the database PATCH went through)", got.Name)
	}
	if opts := got.Properties["Prio"].Options; len(opts) != 0 {
		t.Errorf("Prio.Options = %v, want the previous state: the data source was not written", opts)
	}
	if got.Properties["Hors"].ID != "h1" {
		t.Error("Hors was lost")
	}
}

// First PATCH failing: nothing went through, nothing is recorded.
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
		t.Errorf("Name = %q, want Avant: nothing was written", snap.Databases["tasks"].Name)
	}
}

// fakeTrasher records the ids it is asked to move to the trash. before, when
// set, runs at the start of each call: it is what allows checking that the
// state is already on disk at the time of the next call.
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

// orphanSnapshot anchors each key with the id "db-<key>": a test can thus say
// which id was moved to the trash.
func orphanSnapshot(keys ...string) *state.Snapshot {
	snap := emptySnapshot()
	for _, k := range keys {
		snap.Databases[k] = state.Database{ID: "db-" + k, DataSourceID: "ds-" + k, Name: k}
	}
	return snap
}

// The identity moved to the trash is the state's, and the state is saved
// after EACH destruction: an interruption leaves an exactly true file.
func TestRunTrashesEachOrphanFromItsStateIdentityAndSavesAfterEach(t *testing.T) {
	dir := t.TempDir()
	snap := orphanSnapshot("archive", "tasks")
	// The state is on disk before the first call, as in real life: without it,
	// the read-back below would prove nothing.
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
		t.Errorf("ids moved to the trash = %v, want %v", tr.ids, want)
	}
	if !goneBeforeSecond {
		t.Error("the database.archive entry was still on disk before the second destruction")
	}
	if len(rep.Destroyed) != 2 || !strings.HasPrefix(rep.Destroyed[0], "database.archive moved to the trash") {
		t.Errorf("Destroyed = %v", rep.Destroyed)
	}
	loaded, lerr := state.Load(dir)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if len(loaded.Databases) != 0 {
		t.Errorf("state = %v, want empty", loaded.Databases)
	}
	if !rep.Converged() {
		t.Errorf("Converged() = false: %+v", rep)
	}
}

// Review Focus #1: a 200 that does not confirm the trashing keeps the entry.
// Abandoning it would make a still live database invisible: neither declared,
// nor in the state.
func TestRunKeepsTheStateEntryWhenTheAPIDoesNotConfirmTheTrash(t *testing.T) {
	dir := t.TempDir()
	snap := orphanSnapshot("tasks")
	p := &diff.Plan{Changes: []diff.Change{destroyChange("tasks")}}

	rep, err := Run(context.Background(), p, snap, Options{Dir: dir, Trasher: &fakeTrasher{}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(rep.Destroyed) != 0 {
		t.Errorf("Destroyed = %v, want empty", rep.Destroyed)
	}
	if len(rep.Mismatches) != 1 || !strings.Contains(rep.Mismatches[0], "database.tasks") ||
		!strings.Contains(rep.Mismatches[0], "kept") {
		t.Errorf("Mismatches = %v, want a mismatch naming database.tasks and the kept entry", rep.Mismatches)
	}
	if _, ok := snap.Databases["tasks"]; !ok {
		t.Error("the entry was removed while the trashing is not confirmed")
	}
	if _, serr := os.Stat(state.Path(dir)); !os.IsNotExist(serr) {
		t.Error("a state was written while nothing was destroyed")
	}
	if rep.Converged() {
		t.Error("Converged() = true while the database is not in the trash")
	}
}

// A CONFIRMED trashing whose state write then fails must never suggest that
// nothing happened: the database is already in the trash in Notion, only the
// local record failed.
func TestRunWarnsWhenTrashSucceedsButStateSaveFails(t *testing.T) {
	dir := t.TempDir()
	snap := orphanSnapshot("tasks")
	// The state is on disk before the call, as in real life: without it, the
	// read-back below would prove nothing.
	if err := state.Save(dir, snap); err != nil {
		t.Fatal(err)
	}

	// A non-writable directory fails state.Save after a trashing already
	// confirmed by the API.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skipf("chmod unavailable here: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	p := &diff.Plan{Changes: []diff.Change{destroyChange("tasks")}}
	_, err := Run(context.Background(), p, snap, Options{
		Dir: dir, Trasher: &fakeTrasher{confirmed: true},
	})
	if err == nil {
		_ = os.Chmod(dir, 0o700)
		t.Skip("the directory stays writable (root?), test not meaningful")
	}

	for _, want := range []string{
		"database.tasks", "trash", "  → ", "notion-seed plan",
		"stale state entry", "`apply` will remove",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want containing %q", err.Error(), want)
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
		t.Error("the state on disk lost the entry despite the write failure")
	}
}

// Review Focus #2 and D-B1: no failure removes the entry. The 404 in
// particular does not count as a destruction — it points to the plan, which
// reads the actual state back, and never blames the sharing.
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
			want:     []string{"404", "notion-seed plan", "stale state entry"},
			unwanted: []string{"shared", "integration"},
		},
		{
			name: "archived ancestor",
			err: &transport.APIError{Status: 400, NotionCode: "validation_error",
				Message: "Can't edit page on block with an archived ancestor. You must " +
					"unarchive the ancestor before editing page."},
			want: []string{"ancestor page", "restore the parent page", "rerun apply",
				"permanently delete the parent page", "notion-seed plan",
				"stale state entry", "`apply` will remove"},
		},
		{
			name: "unknown outcome",
			err:  &transport.OutcomeUnknownError{Cause: context.DeadlineExceeded},
			want: []string{"unknown outcome", "notion-seed plan", "stale state entry"},
		},
		{
			name: "generic rejection",
			err: &transport.APIError{Status: 409, NotionCode: "conflict_error",
				Message: "Conflict occurred while saving."},
			want: []string{"failed to", "rerun apply"},
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
				t.Fatal("Run() error = nil, want the trashing failure")
			}
			// The reminder of what is done follows a semicolon: after a period, it
			// would start with a lowercase letter.
			wants := append([]string{"database.tasks", "state entry is kept", "  → ",
				"; no write had succeeded before this one"}, tc.want...)
			for _, want := range wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message = %q, it must contain %q", err.Error(), want)
				}
			}
			for _, unwanted := range tc.unwanted {
				if strings.Contains(err.Error(), unwanted) {
					t.Errorf("message = %q, it must not contain %q", err.Error(), unwanted)
				}
			}
			if _, ok := snap.Databases["tasks"]; !ok {
				t.Error("the entry was removed despite the failure")
			}
			if _, serr := os.Stat(state.Path(dir)); !os.IsNotExist(serr) {
				t.Error("a state was written despite the failure")
			}
		})
	}
}

// A failure in the middle of the series names what was done before it:
// without it, the user does not know which databases are already in the
// trash.
func TestRunNamesTheDestroysAcquiredBeforeAFailure(t *testing.T) {
	dir := t.TempDir()
	snap := orphanSnapshot("archive", "tasks")
	tr := &fakeTrasher{confirmed: true, failAt: 1, err: &transport.APIError{
		Status: 409, NotionCode: "conflict_error", Message: "Conflict occurred while saving."}}
	p := &diff.Plan{Changes: []diff.Change{destroyChange("archive"), destroyChange("tasks")}}

	_, err := Run(context.Background(), p, snap, Options{Dir: dir, Trasher: tr})
	if err == nil {
		t.Fatal("Run() error = nil, want the failure of the second destruction")
	}
	if want := "already moved to the trash and removed from the state: database.archive"; !strings.Contains(err.Error(), want) {
		t.Errorf("message = %q, it must contain %q", err.Error(), want)
	}
	loaded, lerr := state.Load(dir)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if _, ok := loaded.Databases["archive"]; ok {
		t.Error("database.archive is still in the state while it is in the trash")
	}
	if _, ok := loaded.Databases["tasks"]; !ok {
		t.Error("database.tasks left the state while its destruction failed")
	}
}

// Review Focus #4: prevent_destroy is an acknowledgement. Run does not read it,
// and the destruction goes out.
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
		t.Errorf("ids = %v, Destroyed = %v: prevent_destroy must prevent nothing", tr.ids, rep.Destroyed)
	}
}

func TestRunRefusesADestroyWithoutATrasher(t *testing.T) {
	_, err := Run(context.Background(), &diff.Plan{Changes: []diff.Change{destroyChange("tasks")}},
		orphanSnapshot("tasks"), Options{Dir: t.TempDir()})
	if err == nil {
		t.Fatal("error = nil, want a named notion-seed bug")
	}
	for _, want := range []string{"database.tasks", "notion-seed bug", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want containing %q", err, want)
		}
	}
}

// A missing Trasher is seen BEFORE the first write: otherwise a plan that
// creates then destroys would write the creation, then stop on the
// destruction, half applied.
func TestRunRefusesAMissingWriterBeforeAnyWrite(t *testing.T) {
	dir := t.TempDir()
	snap := orphanSnapshot("tasks")
	p := &diff.Plan{Changes: []diff.Change{
		createChange("projects", "Projects"),
		destroyChange("tasks"),
	}}
	rep, err := Run(context.Background(), p, snap, Options{Dir: dir, Creator: refuseToCreate(t)})
	if err == nil {
		t.Fatal("error = nil, want a named notion-seed bug")
	}
	for _, want := range []string{"database.tasks", "notion-seed bug", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want containing %q", err, want)
		}
	}
	if len(rep.Created) != 0 {
		t.Errorf("Created = %v, want no creation before the rejection", rep.Created)
	}
}
