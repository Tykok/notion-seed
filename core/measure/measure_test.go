// SPDX-License-Identifier: GPL-3.0-or-later

package measure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/tykok/notion-seed/core/change"
	"github.com/tykok/notion-seed/core/providers/notion/transport"
)

type transportFunc func(ctx context.Context, req transport.APIRequest) (transport.APIResponse, error)

func (f transportFunc) Execute(ctx context.Context, req transport.APIRequest) (transport.APIResponse, error) {
	return f(ctx, req)
}

// pageOf builds a query response with n results and an optional cursor.
func pageOf(n int, next string) []byte {
	results := make([]map[string]any, n)
	for i := range results {
		results[i] = map[string]any{"object": "page"}
	}
	body := map[string]any{"object": "list", "results": results, "has_more": next != ""}
	if next != "" {
		body["next_cursor"] = next
	}
	b, _ := json.Marshal(body)
	return b
}

// The filter shape must match the type: measured on 2026-09-24, a
// multi_select shape on a select returns 400.
func TestCountBuildsTheFilterMatchingThePropertyType(t *testing.T) {
	tests := []struct {
		propType string
		wantKey  string
		wantOp   string
	}{
		{"select", "select", "equals"},
		{"status", "status", "equals"},
		{"multi_select", "multi_select", "contains"},
	}
	for _, tt := range tests {
		var sent map[string]any
		tr := transportFunc(func(_ context.Context, req transport.APIRequest) (transport.APIResponse, error) {
			_ = json.Unmarshal(req.Body, &sent)
			return transport.APIResponse{Status: 200, Body: pageOf(0, "")}, nil
		})
		_, err := NewCounter(tr).Count(context.Background(), Request{
			DataSourceID: "ds-1", Property: "Statut", PropertyType: tt.propType, Option: "À faire",
		})
		if err != nil {
			t.Fatalf("%s: Count() error = %v", tt.propType, err)
		}
		filter := sent["filter"].(map[string]any)
		if filter["property"] != "Statut" {
			t.Errorf("%s: property = %v", tt.propType, filter["property"])
		}
		cond, ok := filter[tt.wantKey].(map[string]any)
		if !ok {
			t.Fatalf("%s: filter = %v, want a %q key", tt.propType, filter, tt.wantKey)
		}
		if cond[tt.wantOp] != "À faire" {
			t.Errorf("%s: condition = %v, want %s", tt.propType, cond, tt.wantOp)
		}
	}
}

// Empty Option = count the non-empty values of the column, which is what a
// type change needs.
func TestCountWithoutOptionCountsNonEmptyValues(t *testing.T) {
	var sent map[string]any
	tr := transportFunc(func(_ context.Context, req transport.APIRequest) (transport.APIResponse, error) {
		_ = json.Unmarshal(req.Body, &sent)
		return transport.APIResponse{Status: 200, Body: pageOf(2, "")}, nil
	})
	got, err := NewCounter(tr).Count(context.Background(), Request{
		DataSourceID: "ds-1", Property: "Tags", PropertyType: "multi_select",
	})
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	if got.Count != 2 || got.Capped {
		t.Errorf("Result = %+v, want 2 uncapped", got)
	}
	cond := sent["filter"].(map[string]any)["multi_select"].(map[string]any)
	if cond["is_not_empty"] != true {
		t.Errorf("condition = %v, want is_not_empty", cond)
	}
}

// A type with no known filter must not make up a request: it would return
// 400, and a 400 reads as a failure when it is a question notion-seed cannot
// ask.
func TestCountRefusesAPropertyTypeItCannotFilter(t *testing.T) {
	tr := transportFunc(func(context.Context, transport.APIRequest) (transport.APIResponse, error) {
		t.Error("no call should have been made")
		return transport.APIResponse{}, nil
	})
	_, err := NewCounter(tr).Count(context.Background(), Request{
		DataSourceID: "ds-1", Property: "Gens", PropertyType: "people", Option: "X",
	})
	if !errors.Is(err, ErrUnsupportedFilter) {
		t.Errorf("error = %v, want ErrUnsupportedFilter", err)
	}
}

// Pagination is capped: "more than 300" is enough to decide, and a
// 40,000-row database must not make 400 calls to render a plan.
func TestCountStopsAtThePageCap(t *testing.T) {
	calls := 0
	tr := transportFunc(func(context.Context, transport.APIRequest) (transport.APIResponse, error) {
		calls++
		return transport.APIResponse{Status: 200, Body: pageOf(CountPageSize, "curseur")}, nil
	})
	got, err := NewCounter(tr).Count(context.Background(), Request{
		DataSourceID: "ds-1", Property: "Statut", PropertyType: "status", Option: "À faire",
	})
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	if calls != MaxCountedPages {
		t.Errorf("calls = %d, want %d", calls, MaxCountedPages)
	}
	if !got.Capped || got.Count != MaxCountedPages*CountPageSize {
		t.Errorf("Result = %+v, want capped at %d", got, MaxCountedPages*CountPageSize)
	}
}

// The previous page's cursor must be passed on, otherwise the first page is
// recounted forever.
func TestCountPassesTheCursorToTheNextPage(t *testing.T) {
	var cursors []string
	tr := transportFunc(func(_ context.Context, req transport.APIRequest) (transport.APIResponse, error) {
		var body map[string]any
		_ = json.Unmarshal(req.Body, &body)
		c, _ := body["start_cursor"].(string)
		cursors = append(cursors, c)
		if len(cursors) == 1 {
			return transport.APIResponse{Status: 200, Body: pageOf(CountPageSize, "c2")}, nil
		}
		return transport.APIResponse{Status: 200, Body: pageOf(5, "")}, nil
	})
	got, err := NewCounter(tr).Count(context.Background(), Request{
		DataSourceID: "ds-1", Property: "Statut", PropertyType: "status", Option: "X",
	})
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	if len(cursors) != 2 || cursors[0] != "" || cursors[1] != "c2" {
		t.Errorf("cursors = %v, want [\"\", \"c2\"]", cursors)
	}
	if got.Count != CountPageSize+5 || got.Capped {
		t.Errorf("Result = %+v, want %d uncapped", got, CountPageSize+5)
	}
}

func TestCountPropagatesTransportErrors(t *testing.T) {
	tr := transportFunc(func(context.Context, transport.APIRequest) (transport.APIResponse, error) {
		return transport.APIResponse{}, &transport.APIError{Status: 403, NotionCode: "restricted_resource"}
	})
	_, err := NewCounter(tr).Count(context.Background(), Request{
		DataSourceID: "ds-1", Property: "Statut", PropertyType: "status", Option: "X",
	})
	if err == nil {
		t.Fatal("Count() error = nil, want the transport error")
	}
	if !strings.Contains(fmt.Sprint(err), "403") {
		t.Errorf("error = %v, it must carry the status", err)
	}
}

// A 200 response that is NOT a list must be rejected.
//
// Without this rejection, any JSON object decodes without error with a
// missing `results`, hence Count = 0, hence ClassifyOptionRemoval returns
// ClassSafe: notion-seed would claim "0 rows affected, nothing to lose" on a
// response it understood nothing of. That is exactly the unverified claim this
// product exists to make impossible.
func TestCountRefusesAResponseThatIsNotAList(t *testing.T) {
	// A data source's schema: what a wrong route order would return, on the
	// server side as well as in the fake test binary.
	body := []byte(`{"object":"data_source","id":"ds-1",` +
		`"properties":{"Statut":{"id":"p-statut","type":"status"}}}`)
	tr := transportFunc(func(context.Context, transport.APIRequest) (transport.APIResponse, error) {
		return transport.APIResponse{Status: 200, Body: body}, nil
	})

	res, err := NewCounter(tr).Count(context.Background(), Request{
		DataSourceID: "ds-1", Property: "Statut", PropertyType: "status", Option: "Fait",
	})
	if err == nil {
		t.Fatalf("Count() error = nil (Count = %d), want a misunderstood response rejected",
			res.Count)
	}
	if !errors.Is(err, ErrUnreadableCount) {
		t.Errorf("error = %v, want a %v error", err, ErrUnreadableCount)
	}
	if !strings.Contains(fmt.Sprint(err), "  → ") {
		t.Errorf("error = %v, it must carry a corrective action", err)
	}
}

// A list response WITHOUT a `results` field is not an empty list: it is a
// response that was not understood. Telling them apart requires a slice
// pointer — a bare slice confuses "missing" and "empty".
func TestCountRefusesAListWithoutResults(t *testing.T) {
	tr := transportFunc(func(context.Context, transport.APIRequest) (transport.APIResponse, error) {
		return transport.APIResponse{Status: 200, Body: []byte(`{"object":"list","has_more":false}`)}, nil
	})

	res, err := NewCounter(tr).Count(context.Background(), Request{
		DataSourceID: "ds-1", Property: "Statut", PropertyType: "status", Option: "Fait",
	})
	if err == nil {
		t.Fatalf("Count() error = nil (Count = %d), want a list without results rejected",
			res.Count)
	}
	if !errors.Is(err, ErrUnreadableCount) {
		t.Errorf("error = %v, want a %v error", err, ErrUnreadableCount)
	}
}

// An EMPTY list, on the other hand, is a perfectly legitimate response: nobody
// uses the option, and it is the case that makes a removal safe. Confusing it
// with a misunderstood response would defeat the whole point of measuring.
func TestCountAcceptsAnEmptyListAsZero(t *testing.T) {
	tr := transportFunc(func(context.Context, transport.APIRequest) (transport.APIResponse, error) {
		return transport.APIResponse{
			Status: 200,
			Body:   []byte(`{"object":"list","results":[],"has_more":false}`),
		}, nil
	})

	res, err := NewCounter(tr).Count(context.Background(), Request{
		DataSourceID: "ds-1", Property: "Statut", PropertyType: "status", Option: "Fait",
	})
	if err != nil {
		t.Fatalf("Count() error = %v, want an empty list accepted", err)
	}
	if res.Count != 0 {
		t.Errorf("Count = %d, want 0", res.Count)
	}
}

// `has_more: true` with an empty `next_cursor` is a response that was not
// understood: the API announces a next page and does not say where to get it.
//
// Without this rejection, the loop starts over on the FIRST page and recounts
// it on every turn: a 2-row page comes out as {Count: 6, Capped: true}, and the
// user reads "more than 6 rows will be reassigned, without a trace" for 2 real
// rows. Worse than a wrong count: Capped presents it as a lower bound, hence as
// a guarantee.
func TestCountRefusesHasMoreWithoutACursor(t *testing.T) {
	tr := transportFunc(func(context.Context, transport.APIRequest) (transport.APIResponse, error) {
		return transport.APIResponse{Status: 200, Body: []byte(
			`{"object":"list","results":[{"object":"page"},{"object":"page"}],` +
				`"has_more":true,"next_cursor":""}`)}, nil
	})

	res, err := NewCounter(tr).Count(context.Background(), Request{
		DataSourceID: "ds-1", Property: "Statut", PropertyType: "status", Option: "Fait",
	})
	if err == nil {
		t.Fatalf("Count() error = nil (Count = %d, Capped = %v), want a "+
			"has_more without a cursor rejected", res.Count, res.Capped)
	}
	if !errors.Is(err, ErrUnreadableCount) {
		t.Errorf("error = %v, want a %v error", err, ErrUnreadableCount)
	}
	if !strings.Contains(fmt.Sprint(err), "  → ") {
		t.Errorf("error = %v, it must carry a corrective action", err)
	}
}

// AllRows counts ALL the rows of the data source: a database moved to the
// trash takes them all with it, with or without a value in any given column.
// No filter is sent, not even an empty one.
func TestCountAllRowsSendsNoFilter(t *testing.T) {
	var sent map[string]any
	var path string
	tr := transportFunc(func(_ context.Context, req transport.APIRequest) (transport.APIResponse, error) {
		path = req.Path
		_ = json.Unmarshal(req.Body, &sent)
		return transport.APIResponse{Status: 200, Body: pageOf(3, "")}, nil
	})
	res, err := NewCounter(tr).Count(context.Background(), Request{DataSourceID: "ds-1", AllRows: true})
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	if _, ok := sent["filter"]; ok {
		t.Errorf("body = %v, want no filter", sent)
	}
	if path != "/v1/data_sources/ds-1/query" {
		t.Errorf("path = %q", path)
	}
	if res.Count != 3 || res.Capped {
		t.Errorf("Result = %+v, want {3 false}", res)
	}
}

// A failed destruction count does not make the line "unknown": it stays
// destructive. Its message must not announce the opposite.
func TestCountAllRowsFailureDoesNotPromiseAnUnknownImpact(t *testing.T) {
	failures := map[string]transportFunc{
		"transport": func(context.Context, transport.APIRequest) (transport.APIResponse, error) {
			return transport.APIResponse{}, errors.New("403")
		},
		"unreadable": func(context.Context, transport.APIRequest) (transport.APIResponse, error) {
			return transport.APIResponse{Status: 200, Body: []byte("not json")}, nil
		},
		"not a list": func(context.Context, transport.APIRequest) (transport.APIResponse, error) {
			return transport.APIResponse{Status: 200, Body: []byte(`{"object":"data_source"}`)}, nil
		},
		"no results": func(context.Context, transport.APIRequest) (transport.APIResponse, error) {
			return transport.APIResponse{Status: 200, Body: []byte(`{"object":"list"}`)}, nil
		},
		"no cursor": func(context.Context, transport.APIRequest) (transport.APIResponse, error) {
			return transport.APIResponse{Status: 200, Body: []byte(`{"object":"list","results":[],"has_more":true}`)}, nil
		},
	}
	for name, tr := range failures {
		_, err := NewCounter(tr).Count(context.Background(), Request{DataSourceID: "ds-1", AllRows: true})
		if err == nil {
			t.Fatalf("%s: Count() error = nil", name)
		}
		if strings.Contains(err.Error(), "unknown") {
			t.Errorf("%s: message = %q, it announces an unknown impact", name, err.Error())
		}
		if !strings.Contains(err.Error(), "still announced as destructive, without its number of rows") {
			t.Errorf("%s: message = %q, it must say the destruction stays destructive", name, err.Error())
		}
	}
}

// sentFilter runs one count and returns the filter sent, as JSON, or "none".
func sentFilter(t *testing.T, r Request) string {
	t.Helper()
	var sent map[string]json.RawMessage
	tr := transportFunc(func(_ context.Context, req transport.APIRequest) (transport.APIResponse, error) {
		_ = json.Unmarshal(req.Body, &sent)
		return transport.APIResponse{Status: 200, Body: pageOf(0, "")}, nil
	})
	if _, err := NewCounter(tr).Count(context.Background(), r); err != nil {
		t.Fatalf("Count(%+v) error = %v", r, err)
	}
	if f, ok := sent["filter"]; ok {
		return string(f)
	}
	return "none"
}

// The type-change filters of the 2026-09-25 campaign, on the SOURCE column.
func TestCountBuildsTheTypeChangeFilters(t *testing.T) {
	tests := []struct {
		name string
		r    Request
		want string
	}{
		{"non-empty on any filterable type",
			Request{Property: "N", PropertyType: "date"},
			`{"date":{"is_not_empty":true},"property":"N"}`},
		{"checked rows",
			Request{Property: "C", PropertyType: "checkbox", Count: change.CountChecked},
			`{"checkbox":{"equals":true},"property":"C"}`},
		{"unchecked rows",
			Request{Property: "C", PropertyType: "checkbox", Count: change.CountUnchecked},
			`{"checkbox":{"equals":false},"property":"C"}`},
		{"empty rows",
			Request{Property: "S", PropertyType: "select", Count: change.CountEmpty},
			`{"property":"S","select":{"is_empty":true}}`},
		{"every row: no filter at all",
			Request{Property: "S", PropertyType: "date", Count: change.CountEveryRow},
			"none"},
		{"non-empty except the declared options",
			Request{Property: "T", PropertyType: "rich_text", Except: []string{"Un", "Deux"}},
			`{"and":[{"property":"T","rich_text":{"is_not_empty":true}},` +
				`{"property":"T","rich_text":{"does_not_equal":"Un"}},` +
				`{"property":"T","rich_text":{"does_not_equal":"Deux"}}]}`},
		{"a number option filters as a number",
			Request{Property: "N", PropertyType: "number", Except: []string{"-3.5"}},
			`{"and":[{"number":{"is_not_empty":true},"property":"N"},` +
				`{"number":{"does_not_equal":-3.5},"property":"N"}]}`},
		{"every row except the declared options",
			Request{Property: "U", PropertyType: "url", Count: change.CountEveryRow, Except: []string{"a"}},
			`{"or":[{"property":"U","url":{"is_empty":true}},` +
				`{"and":[{"property":"U","url":{"is_not_empty":true}},` +
				`{"property":"U","url":{"does_not_equal":"a"}}]}]}`},
	}
	for _, tt := range tests {
		tt.r.DataSourceID = "ds-1"
		if got := sentFilter(t, tt.r); got != tt.want {
			t.Errorf("%s:\n got %s\nwant %s", tt.name, got, tt.want)
		}
	}
}

// No sound filter: nothing is sent, and the question is reported as one
// notion-seed cannot ask — not as an outage.
func TestCountRefusesAnUnsoundTypeChangeCount(t *testing.T) {
	tr := transportFunc(func(context.Context, transport.APIRequest) (transport.APIResponse, error) {
		t.Error("no call should have been made")
		return transport.APIResponse{}, nil
	})
	for _, r := range []Request{
		{DataSourceID: "ds-1", Property: "T", PropertyType: "rich_text", Count: change.CountUnsound},
		{DataSourceID: "ds-1", Property: "T", PropertyType: "title"},
		{DataSourceID: "ds-1", Property: "C", PropertyType: "checkbox"},
	} {
		if _, err := NewCounter(tr).Count(context.Background(), r); !errors.Is(err, ErrUnsupportedFilter) {
			t.Errorf("%+v: error = %v, want ErrUnsupportedFilter", r, err)
		}
	}
}
