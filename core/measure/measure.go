// SPDX-License-Identifier: GPL-3.0-or-later

// Package measure counts, READ-ONLY, the rows a change is going to touch.
//
// It is what lets the product stop refusing on principle: blocking was a
// substitute for knowledge, and the API can answer. notion-seed reads the rows
// to say what a change costs; it never writes any.
package measure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/tykok/notion-seed/core/change"
	"github.com/tykok/notion-seed/core/providers/notion/transport"
)

// CountPageSize and MaxCountedPages cap the pagination.
//
// The exact count is what drives the decision — "47 rows" has nothing to do
// with "at least one". But a 40,000-row database must not cost 400 calls to
// render a plan: beyond the cap, "more than 300" is plenty to decide, and the
// Result says so.
const (
	CountPageSize   = 100
	MaxCountedPages = 3
)

// ErrUnsupportedFilter reports a property type notion-seed cannot build a
// filter for. Measured on 2026-09-24: a filter shape that does not match the
// type returns 400. Making up a request would therefore pass off "I cannot ask
// the question" as "the API rejected it".
var ErrUnsupportedFilter = errors.New("non-filterable property type")

// ErrUnreadableCount reports a count response that was not understood.
//
// It exists because the defect it closes is silent: any JSON object decodes
// into a list structure with a missing `results`, hence a count of 0, hence
// "no rows affected", hence "safe". A misunderstood response must return an
// ERROR, never a count — that is exactly the unverified claim notion-seed
// exists to make impossible.
var ErrUnreadableCount = errors.New("misunderstood count response")

// Request describes ONE measurement. An empty Option means "count the
// non-empty values of the column", which is what a type change needs.
//
// AllRows counts every row of the data source, without a filter: it is what a
// destruction takes with it. Property, PropertyType and Option are then
// ignored.
//
// Count and Except describe a type change's count, when Option is empty: see
// change.Count. The zero Count is the non-empty values of the column.
type Request struct {
	DataSourceID string
	Property     string
	PropertyType string
	Option       string
	AllRows      bool
	Count        change.Count
	Except       []string
}

// fallback says what the plan will show without a count. It differs for a
// destruction: its class does not depend on the count, it stays destructive,
// and announcing it as "unknown" would be wrong.
func (r Request) fallback() string {
	if r.AllRows {
		return "the destruction is still announced as destructive, without its number of rows"
	}
	return "the impact of this change will be announced as unknown"
}

// subject names what is being counted, for error messages.
func (r Request) subject() string {
	if r.AllRows {
		return "the database"
	}
	return fmt.Sprintf("%q", r.Property)
}

// Result holds the count. Capped says the pagination cap was reached and that
// Count is therefore a lower bound, not a count.
type Result struct {
	Count  int
	Capped bool
}

// Counter is what the plan needs. The interface is declared here, on the
// consumer side, so the tests do not have to build a transport stack.
type Counter interface {
	Count(ctx context.Context, r Request) (Result, error)
}

// NotionCounter counts through the API.
type NotionCounter struct {
	tr transport.Transport
}

func NewCounter(tr transport.Transport) *NotionCounter {
	return &NotionCounter{tr: tr}
}

var _ Counter = (*NotionCounter)(nil)

// filterFor builds the filter matching the property type. A nil filter with
// no error counts every row.
//
// Measured on 2026-09-24: select and status filter with `equals`, multi_select
// with `contains`. The shape MUST match the type, otherwise 400.
func filterFor(r Request) (map[string]any, error) {
	if r.Option == "" {
		return typeChangeFilter(r)
	}
	var op string
	switch r.PropertyType {
	case "select", "status":
		op = "equals"
	case "multi_select":
		op = "contains"
	default:
		return nil, unsupported(r.PropertyType)
	}
	return map[string]any{
		"property":     r.Property,
		r.PropertyType: map[string]any{op: r.Option},
	}, nil
}

func unsupported(propType string) error {
	return fmt.Errorf("%w: %q\n"+
		"  → notion-seed cannot count the rows of this type; the impact "+
		"will be announced as unknown rather than guessed",
		ErrUnsupportedFilter, propType)
}

// emptiable lists the source types whose rows filter with `is_empty` and
// `is_not_empty`. checkbox has no empty state, and title never changes type.
var emptiable = map[string]bool{
	"rich_text": true, "number": true, "url": true, "select": true,
	"status": true, "multi_select": true, "date": true, "people": true,
}

// typeChangeFilter builds the count of a type change, per the filters the
// 2026-09-25 campaign checked against the rows actually touched.
//
// Shapes measured on 2026-09-25 ("formes de filtre", and its sequel):
// `or[is_empty, and[does_not_equal…]]` is accepted and correct on select,
// rich_text and multi_select (`does_not_contain`); one more level of nesting
// is refused with 400, so nothing here goes deeper than or → and. On rich_text
// and url, `does_not_equal` ignores case and trailing spaces (url: not a
// trailing slash) while a conversion keeps only an EXACT match: a count
// excluding declared names is a lower bound — change.TypeChangeOf says so. On
// number, `does_not_equal` is exact and numeric. On number and url it MATCHES
// EMPTY ROWS, which is why every exclusion goes with `is_not_empty`.
// `checkbox equals false` counts exactly the unchecked rows.
func typeChangeFilter(r Request) (map[string]any, error) {
	on := func(cond map[string]any) map[string]any {
		return map[string]any{"property": r.Property, r.PropertyType: cond}
	}
	switch r.Count {
	case change.CountChecked, change.CountUnchecked:
		if r.PropertyType != "checkbox" {
			return nil, unsupported(r.PropertyType)
		}
		return on(map[string]any{"equals": r.Count == change.CountChecked}), nil
	case change.CountUnsound:
		return nil, unsupported(r.PropertyType)
	}
	if !emptiable[r.PropertyType] {
		return nil, unsupported(r.PropertyType)
	}

	switch r.Count {
	case change.CountEmpty:
		return on(map[string]any{"is_empty": true}), nil
	case change.CountEveryRow:
		// No filter at all — not even an empty one, whose API behavior has not
		// been measured.
		if len(r.Except) == 0 {
			return nil, nil
		}
		// An empty row gets a value too: it is counted apart, since whether
		// `does_not_equal` matches an empty value was not measured.
		rest, err := exceptFilter(r, on)
		if err != nil {
			return nil, err
		}
		return map[string]any{"or": []any{on(map[string]any{"is_empty": true}), rest}}, nil
	case change.CountNonEmpty:
		if len(r.Except) == 0 {
			return on(map[string]any{"is_not_empty": true}), nil
		}
		return exceptFilter(r, on)
	}
	return nil, unsupported(r.PropertyType)
}

// exceptFilter counts the non-empty rows whose value is none of r.Except.
func exceptFilter(r Request, on func(map[string]any) map[string]any) (map[string]any, error) {
	and := []any{on(map[string]any{"is_not_empty": true})}
	for _, name := range r.Except {
		var v any = name
		if r.PropertyType == "number" {
			f, err := strconv.ParseFloat(name, 64)
			if err != nil {
				return nil, fmt.Errorf("%w: option %q is not a number\n"+
					"  → this is a notion-seed bug, not a configuration error: "+
					"report it", ErrUnsupportedFilter, name)
			}
			v = f
		}
		op := "does_not_equal"
		if r.PropertyType == "multi_select" {
			op = "does_not_contain"
		}
		and = append(and, on(map[string]any{op: v}))
	}
	return map[string]any{"and": and}, nil
}

// Count returns the number of rows affected, capped.
func (c *NotionCounter) Count(ctx context.Context, r Request) (Result, error) {
	// No filter at all for AllRows — not even an empty filter, whose API
	// behavior has not been measured. filterFor also returns none for a type
	// change that touches every row.
	var filter map[string]any
	if !r.AllRows {
		var err error
		if filter, err = filterFor(r); err != nil {
			return Result{}, err
		}
	}

	var out Result
	cursor := ""
	for page := 0; page < MaxCountedPages; page++ {
		body := map[string]any{"page_size": CountPageSize}
		if filter != nil {
			body["filter"] = filter
		}
		if cursor != "" {
			body["start_cursor"] = cursor
		}
		raw, err := json.Marshal(body)
		if err != nil {
			return Result{}, fmt.Errorf(
				"failed to build the count query: %w\n"+
					"  → this is a notion-seed bug, not a configuration error: "+
					"report it", err)
		}

		resp, err := c.tr.Execute(ctx, transport.APIRequest{
			Method: "POST",
			Path:   "/v1/data_sources/" + r.DataSourceID + "/query",
			Body:   raw,
		})
		if err != nil {
			return Result{}, fmt.Errorf(
				"failed to count the rows of %s: %w\n"+
					"  → %s; retry "+
					"to get the count", r.subject(), err, r.fallback())
		}

		// Results is a slice POINTER, deliberately: a bare slice confuses
		// "results missing" (misunderstood response) with "results empty"
		// (nobody uses the option), and these two cases must end at opposite
		// ends — an error for the first, a count of 0 for the second.
		var decoded struct {
			Object     string             `json:"object"`
			Results    *[]json.RawMessage `json:"results"`
			HasMore    bool               `json:"has_more"`
			NextCursor string             `json:"next_cursor"`
		}
		if err := json.Unmarshal(resp.Body, &decoded); err != nil {
			return Result{}, fmt.Errorf(
				"unreadable count response: %w\n"+
					"  → retry; if it persists, %s", err, r.fallback())
		}

		// A 200 whose shape is not recognized is NOT worth zero rows. Without
		// these two guards, a data source's schema — what a misordered route
		// returns — decodes without error and makes the plan announce "0 rows
		// affected, nothing to lose" on a response nothing was understood of.
		if decoded.Object != "list" {
			return Result{}, fmt.Errorf(
				"%w for %s: the API answered a %q object, not a list of rows\n"+
					"  → %s; retry, "+
					"and if it persists report it: notion-seed no longer recognizes the "+
					"API response",
				ErrUnreadableCount, r.subject(), decoded.Object, r.fallback())
		}
		if decoded.Results == nil {
			return Result{}, fmt.Errorf(
				"%w for %s: the list returned by the API has no results field\n"+
					"  → %s; retry, "+
					"and if it persists report it: notion-seed no longer recognizes the "+
					"API response",
				ErrUnreadableCount, r.subject(), r.fallback())
		}

		out.Count += len(*decoded.Results)
		if !decoded.HasMore {
			return out, nil
		}
		// A next page announced without saying where to get it is a
		// misunderstood response, just like a missing `results`. Starting over
		// without a cursor would ask for the FIRST page again and recount it on
		// every turn: a 2-row page would come out as "more than 6 rows", a
		// made-up number that Capped moreover presents as a lower bound — hence
		// as a guarantee. Better to announce nothing than to guarantee an
		// invented number.
		if decoded.NextCursor == "" {
			return Result{}, fmt.Errorf(
				"%w for %s: the API announces a next page without a cursor to reach it\n"+
					"  → %s; retry, "+
					"and if it persists report it: notion-seed no longer recognizes the "+
					"API response",
				ErrUnreadableCount, r.subject(), r.fallback())
		}
		cursor = decoded.NextCursor
	}

	out.Capped = true
	return out, nil
}
