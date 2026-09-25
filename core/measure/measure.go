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
type Request struct {
	DataSourceID string
	Property     string
	PropertyType string
	Option       string
	AllRows      bool
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

// filterFor builds the filter matching the property type.
//
// Measured on 2026-09-24: select and status filter with `equals`, multi_select
// with `contains`. The shape MUST match the type, otherwise 400.
func filterFor(r Request) (map[string]any, error) {
	var op string
	switch r.PropertyType {
	case "select", "status":
		op = "equals"
	case "multi_select":
		op = "contains"
	default:
		return nil, fmt.Errorf("%w: %q\n"+
			"  → notion-seed cannot count the rows of this type; the impact "+
			"will be announced as unknown rather than guessed",
			ErrUnsupportedFilter, r.PropertyType)
	}

	cond := map[string]any{op: r.Option}
	if r.Option == "" {
		cond = map[string]any{"is_not_empty": true}
	}
	return map[string]any{
		"property":     r.Property,
		r.PropertyType: cond,
	}, nil
}

// Count returns the number of rows affected, capped.
func (c *NotionCounter) Count(ctx context.Context, r Request) (Result, error) {
	// No filter at all for AllRows — not even an empty filter, whose API
	// behavior has not been measured.
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
