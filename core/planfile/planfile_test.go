// SPDX-License-Identifier: GPL-3.0-or-later

package planfile

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tykok/notion-seed/core/change"
	"github.com/tykok/notion-seed/core/diff"
	"github.com/tykok/notion-seed/core/providers/notion/resources"
	"github.com/tykok/notion-seed/core/state"
)

const testVersion = "0.9.0"

func testMeta() Meta {
	return Meta{
		Version:      testVersion,
		WorkspaceID:  "33333333-3333-4333-8333-333333333333",
		ConfigSHA256: "c0ffee",
		StateSHA256:  "5ca1ab1e",
		CreatedAt:    time.Date(2026, 9, 25, 14, 0, 0, 0, time.UTC),
		Rendered:     "Plan: 0 to add, 1 to change, 0 to destroy\n",
	}
}

// measured builds a detail as the measurement pass leaves it.
func measured(op, target string, class change.Class, m resources.Measurement, count int, capped bool) resources.Detail {
	d := resources.NewDetail(op, target, class)
	d.Measure = &m
	d.Count = count
	d.Capped = capped
	return d
}

// tasksPlan is an update of database.tasks: a free line, a counted removal,
// and a stale state entry.
func tasksPlan(count int) *diff.Plan {
	return &diff.Plan{
		ToChange: 1,
		Changes: []diff.Change{{
			Resource: "database.tasks",
			Key:      "tasks",
			Kind:     resources.KindUpdate,
			Class:    change.ClassDestructive,
			// Target carries identities and payload material: it must never
			// reach the file.
			Target: &state.Database{ID: "db-secret-id", DataSourceID: "ds-secret-id"},
			Details: []resources.Detail{
				resources.NewDetail("+", `property "Estimate" (number)`, change.ClassSafe),
				measured("-", `option "High" (property "Prio")`, change.ClassDestructive,
					resources.Measurement{Property: "Prio", PropertyType: "select", Option: "High"},
					count, false),
			},
		}},
		StaleState: []string{"database.old"},
	}
}

func intPtr(n int) *int { return &n }

func TestEncodeThenDecodeRoundTrips(t *testing.T) {
	data, err := Encode(tasksPlan(12), testMeta())
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	got, err := Decode(data, testVersion)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	want := File{
		Format:       Format,
		NotionSeed:   testVersion,
		WorkspaceID:  "33333333-3333-4333-8333-333333333333",
		CreatedAt:    time.Date(2026, 9, 25, 14, 0, 0, 0, time.UTC),
		ConfigSHA256: "c0ffee",
		StateSHA256:  "5ca1ab1e",
		Changes: []Change{{
			Resource: "database.tasks",
			Kind:     "update",
			Details: []Line{
				{Op: "+", Target: `property "Estimate" (number)`, Class: "safe"},
				{Op: "-", Target: `option "High" (property "Prio")`, Class: "destructive",
					Count: intPtr(12), Bound: BoundExact},
			},
		}},
		StaleState: []string{"database.old"},
		Rendered:   "Plan: 0 to add, 1 to change, 0 to destroy\n",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Decode(Encode()) =\n%+v\nwant\n%+v", got, want)
	}
}

// The file can live in a CI artifact: it carries what the plan shows, never an
// identity nor a payload.
func TestEncodeCarriesNoIdentityNorPayload(t *testing.T) {
	data, err := Encode(tasksPlan(12), testMeta())
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"db-secret-id", "ds-secret-id"} {
		if strings.Contains(string(data), leak) {
			t.Errorf("the plan file carries %q:\n%s", leak, data)
		}
	}
}

// Encode is deterministic: the same plan gives the same bytes, so a CI can
// compare two files.
func TestEncodeIsDeterministic(t *testing.T) {
	first, err := Encode(tasksPlan(12), testMeta())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := Encode(tasksPlan(12), testMeta())
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(first) {
			t.Fatalf("run %d differs:\n%s\n---\n%s", i, first, again)
		}
	}
	if !strings.HasSuffix(string(first), "}\n") {
		t.Errorf("the file does not end with a newline")
	}
}

// Review Focus #4: an empty plan is a plan. Its file says "no change" in
// lists, not in nulls, and reads back as such.
func TestEncodeAnEmptyPlanWritesEmptyLists(t *testing.T) {
	data, err := Encode(&diff.Plan{}, testMeta())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"changes": []`, `"stale_state": []`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("file:\n%s\nwant %s", data, want)
		}
	}
	f, err := Decode(data, testVersion)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Changes) != 0 || len(f.StaleState) != 0 {
		t.Errorf("Decode() = %+v, want no change", f)
	}
}

// Every kind of figure has its bound in the file, and the file carries a
// count exactly when the plan printed one.
func TestEncodeNamesWhatEachFigureGuarantees(t *testing.T) {
	typeChange := resources.Measurement{Property: "Prio", PropertyType: "rich_text"}
	atLeast := typeChange
	atLeast.Bound = change.BoundAtLeast
	atMost := typeChange
	atMost.Bound = change.BoundAtMost
	destroy := resources.Measurement{AllRows: true}
	partial := resources.Measurement{AllRows: true, UncountedDataSources: 1}

	for _, tc := range []struct {
		name      string
		detail    resources.Detail
		wantBound Bound
		wantCount *int
	}{
		{"no measurement", resources.NewDetail("+", "x", change.ClassSafe), BoundNone, nil},
		{"exact", measured("~", "x", change.ClassDestructive, typeChange, 7, false), BoundExact, intPtr(7)},
		{"exact zero", measured("~", "x", change.ClassSafe, typeChange, 0, false), BoundExact, intPtr(0)},
		{"up to", measured("~", "x", change.ClassDestructive, atMost, 7, false), BoundAtMost, intPtr(7)},
		{"at least", measured("~", "x", change.ClassDestructive, atLeast, 7, false), BoundAtLeast, intPtr(7)},
		{"at least zero", measured("~", "x", change.ClassDestructive, atLeast, 0, false), BoundAtLeast, intPtr(0)},
		{"capped", measured("~", "x", change.ClassDestructive, typeChange, 300, true), BoundMoreThan, intPtr(300)},
		{"capped up to", measured("~", "x", change.ClassDestructive, atMost, 300, true), BoundMoreThan, intPtr(300)},
		{"destroy", measured("-", "x", change.ClassDestructive, destroy, 3, false), BoundExact, intPtr(3)},
		{"destroy partial", measured("-", "x", change.ClassDestructive, partial, 3, false), BoundAtLeast, intPtr(3)},
		{"destroy partial capped", measured("-", "x", change.ClassDestructive, partial, 300, true), BoundMoreThan, intPtr(300)},
		{"not measured", measured("~", "x", change.ClassUnknownImpact, typeChange, -1, false), BoundUnmeasured, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := lineOf(tc.detail)
			if got.Bound != tc.wantBound {
				t.Errorf("Bound = %q, want %q", got.Bound, tc.wantBound)
			}
			if !reflect.DeepEqual(got.Count, tc.wantCount) {
				t.Errorf("Count = %v, want %v", got.Count, tc.wantCount)
			}
		})
	}
}

// M1: the source type of a property type change lives on resources.Detail
// (threeway.typeChangeLines sets FromType even on a safe pair, where Measure
// stays nil — Note is free text and is not compared). lineOf carries it into
// the line as From, and only for a type-change detail: every other detail
// carries none, so a same-type update is never mistaken for one.
func TestLineOfRecordsTheSourceTypeOfATypeChangeOnly(t *testing.T) {
	typeChange := resources.NewDetail("~", `property "Prio"`, change.ClassSafe)
	typeChange.FromType = "select"
	if got := lineOf(typeChange).From; got != "select" {
		t.Errorf("From = %q, want %q", got, "select")
	}

	notTypeChange := resources.NewDetail("+", `property "Estimate" (number)`, change.ClassSafe)
	if got := lineOf(notTypeChange).From; got != "" {
		t.Errorf("From = %q, want none", got)
	}
}

// M1: Encode then Decode round-trips the source type of a type change.
func TestEncodeThenDecodeRoundTripsTheSourceTypeOfATypeChange(t *testing.T) {
	typeChange := resources.NewDetail("~", `property "Prio"`, change.ClassSafe)
	typeChange.FromType = "select"
	plan := &diff.Plan{
		ToChange: 1,
		Changes: []diff.Change{{
			Resource: "database.tasks",
			Key:      "tasks",
			Kind:     resources.KindUpdate,
			Class:    change.ClassSafe,
			Details:  []resources.Detail{typeChange},
		}},
	}
	data, err := Encode(plan, testMeta())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"from": "select"`) {
		t.Errorf("file:\n%s\nwant a \"from\" field", data)
	}
	got, err := Decode(data, testVersion)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Changes) != 1 || len(got.Changes[0].Details) != 1 {
		t.Fatalf("Decode() = %+v", got)
	}
	if from := got.Changes[0].Details[0].From; from != "select" {
		t.Errorf("From = %q, want %q", from, "select")
	}
}

// A line that is not a type change carries no From: omitempty keeps the file
// free of a field that would say nothing.
func TestEncodeOmitsFromWhenThereIsNoTypeChange(t *testing.T) {
	data, err := Encode(tasksPlan(12), testMeta())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"from"`) {
		t.Errorf("file carries a \"from\" field with no type change:\n%s", data)
	}
}

func TestDecodeRefusesAnUnknownFormat(t *testing.T) {
	_, err := Decode([]byte(`{"format":2,"notion_seed":"0.9.0"}`), testVersion)
	if err == nil {
		t.Fatal("Decode() error = nil, want a refusal")
	}
	for _, want := range []string{"format 2", "format 1", "notion-seed plan --out", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, want %q", err.Error(), want)
		}
	}
}

// P6: the version is part of the contract.
func TestDecodeRefusesAnotherVersion(t *testing.T) {
	data, err := Encode(tasksPlan(12), testMeta())
	if err != nil {
		t.Fatal(err)
	}
	_, err = Decode(data, "0.10.0")
	if err == nil {
		t.Fatal("Decode() error = nil, want a refusal")
	}
	for _, want := range []string{"0.9.0", "0.10.0", "notion-seed plan --out", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, want %q", err.Error(), want)
		}
	}
}

func TestDecodeRefusesWhatIsNotAPlanFile(t *testing.T) {
	for name, data := range map[string]string{
		"not json":      "Plan: 1 to add",
		"no format":     `{"notion_seed":"0.9.0"}`,
		"unknown field": `{"format":1,"notion_seed":"0.9.0","payload":{}}`,
		"wrong type":    `{"format":1,"notion_seed":"0.9.0","changes":"all"}`,
		"unknown kind": `{"format":1,"notion_seed":"0.9.0","changes":[` +
			`{"resource":"database.tasks","kind":"rename","withheld":"","details":[]}]}`,
		"unknown bound": `{"format":1,"notion_seed":"0.9.0","changes":[` +
			`{"resource":"database.tasks","kind":"update","withheld":"","details":[` +
			`{"op":"-","target":"x","class":"destructive","count":3,"bound":"roughly"}]}]}`,
		"bound without count": `{"format":1,"notion_seed":"0.9.0","changes":[` +
			`{"resource":"database.tasks","kind":"update","withheld":"","details":[` +
			`{"op":"-","target":"x","class":"destructive","bound":"exact"}]}]}`,
		"count without bound": `{"format":1,"notion_seed":"0.9.0","changes":[` +
			`{"resource":"database.tasks","kind":"update","withheld":"","details":[` +
			`{"op":"-","target":"x","class":"destructive","count":3}]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Decode([]byte(data), testVersion)
			if err == nil {
				t.Fatal("Decode() error = nil, want a refusal")
			}
			if !strings.Contains(err.Error(), "  → ") {
				t.Errorf("message = %q, want a corrective action", err.Error())
			}
		})
	}
}

func TestWriteFileReplacesTheFileAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.out")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("new\n")); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new\n" {
		t.Errorf("content = %q, want %q", got, "new\n")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644", info.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("directory holds %d entries, want only plan.out: a temporary file stayed", len(entries))
	}
}

func TestWriteFileNamesTheMissingDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "plan.out")
	err := WriteFile(path, []byte("x"))
	if err == nil {
		t.Fatal("WriteFile() error = nil, want a failure")
	}
	for _, want := range []string{path, "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, want %q", err.Error(), want)
		}
	}
}
