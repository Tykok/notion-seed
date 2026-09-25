// SPDX-License-Identifier: GPL-3.0-or-later

package resources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/tykok/notion-seed/core/change"
	"github.com/tykok/notion-seed/core/config"
	"github.com/tykok/notion-seed/core/providers/notion/transport"
)

// RemoteOption is a select/status/multi_select option as it exists in Notion.
// The ID is the only identity anchor: re-adding an option by its name after
// destroying it creates a new one, with a new ID.
type RemoteOption struct {
	ID    string
	Name  string
	Color string
	Group string
}

// RemoteProperty is a property as it exists in Notion.
type RemoteProperty struct {
	ID           string
	Type         string
	NumberFormat string
	Options      []RemoteOption
}

// RemoteDatabase is the remote state of a database and its default data
// source. Both are modeled together in MVP 0, which handles only one data
// source per database, but the identities stay distinct.
type RemoteDatabase struct {
	ID           string
	DataSourceID string
	Name         string
	Description  string
	// Icon is the database's emoji, or "" for any other form of icon. The
	// YAML only declares an emoji: decoding a file or a URL here would produce
	// a difference the plan would show on every run without ever being able
	// to resolve it.
	//
	// Measured on 2026-09-24: the icon is NOT shared between the database and
	// its data source. PATCH on the database updates both, PATCH on the data
	// source touches only the latter. notion-seed reads and writes the
	// database's one.
	Icon       string
	Properties map[string]RemoteProperty
	Archived   bool
	Found      bool
	// DataSourceCount is the number of data sources the database holds. The
	// MVP reads only one, DataSourceID, but a trashing takes them all: without
	// this number, a destruction's row count would read as complete while it
	// covers only one data source.
	DataSourceCount int
}

func (d RemoteDatabase) Exists() bool { return d.Found }

// DatabaseResource reads and compares Notion databases.
type DatabaseResource struct {
	tr     transport.Transport
	decode func(dbBody, dsBody []byte) (RemoteDatabase, error)
}

var _ Resource = (*DatabaseResource)(nil)

// NewDatabaseResource builds the resource. The decoder is injected to avoid
// an import cycle between resources and mapper.
func NewDatabaseResource(
	tr transport.Transport,
	decode func(dbBody, dsBody []byte) (RemoteDatabase, error),
) *DatabaseResource {
	return &DatabaseResource{tr: tr, decode: decode}
}

func (r *DatabaseResource) Type() string { return "database" }

// Read reads a database and its default data source. Two calls are needed:
// the database holds the identity and the archiving, the data source holds
// the title and the schema.
//
// Not called by `plan` in MVP 0 (no resource is matched without a state); it
// is the seam of the MVP 1 refresh and of `import`.
func (r *DatabaseResource) Read(ctx context.Context, id string) (RemoteState, error) {
	dbResp, err := r.tr.Execute(ctx, transport.APIRequest{
		Method: "GET",
		Path:   "/v1/databases/" + id,
	})
	if err != nil {
		return RemoteDatabase{}, err
	}
	// This minimal parsing duplicates a field the decoder will read again. It
	// is structural, not accidental: the data source id is needed BEFORE the
	// second call can be made, while the decoder needs both bodies — so it
	// cannot run first. Exporting a helper from mapper would recreate the
	// import cycle the injection breaks, and injecting a second function for a
	// single field would cost more than the duplication.
	// TestProbeAndDecoderAgreeOnDataSourceID, in the mapper package, keeps the
	// two shapes in sync by running the real decoder.
	var probe struct {
		DataSources []struct {
			ID string `json:"id"`
		} `json:"data_sources"`
	}
	if err := json.Unmarshal(dbResp.Body, &probe); err != nil {
		return RemoteDatabase{}, fmt.Errorf(
			"unreadable response from GET /v1/databases/%s: %w\n"+
				"  → retry; if it persists, check that `ntn` speaks API version "+
				"2025-09-03 (`ntn --version`)", id, err)
	}
	if len(probe.DataSources) == 0 {
		return RemoteDatabase{}, fmt.Errorf(
			"database %s exposes no data source, which should not happen\n"+
				"  → check that the id designates a database (and not a page) in "+
				"the Notion URL; if it does, report it: notion-seed cannot "+
				"read this response shape", id)
	}

	dsResp, err := r.tr.Execute(ctx, transport.APIRequest{
		Method: "GET",
		Path:   "/v1/data_sources/" + probe.DataSources[0].ID,
	})
	if err != nil {
		return RemoteDatabase{}, err
	}
	return r.decode(dbResp.Body, dsResp.Body)
}

// DatabaseExists probes the EXISTENCE of a database, without reading anything
// else.
//
// Unlike Read, it does NOT reach the data source: Task 8 uses it to diagnose
// an archived ancestor after the data source PATCH failed with a 404. Under an
// ancestor in the trash, GET database answers 200 while the data source is
// unreachable — a full Read would fail here and hide the diagnosis behind the
// failure of the second request.
func (r *DatabaseResource) DatabaseExists(ctx context.Context, id string) (bool, error) {
	_, err := r.tr.Execute(ctx, transport.APIRequest{
		Method: "GET",
		Path:   "/v1/databases/" + id,
	})
	if err == nil {
		return true, nil
	}
	var apiErr *transport.APIError
	if errors.As(err, &apiErr) && apiErr.Status == 404 {
		return false, nil
	}
	return false, err
}

// CreatedDatabase holds the result of a creation.
//
// ID and DataSourceID are filled in as soon as the POST has answered, EVEN if
// the read-back fails afterwards: a lost identity costs a database re-created
// as a duplicate on the next apply, while an incomplete snapshot only costs
// an undetectable drift on the options. ReadErr holds the read-back failure,
// which is not a creation failure — confusing the two would suggest nothing
// was written.
type CreatedDatabase struct {
	ID           string
	DataSourceID string
	Remote       RemoteDatabase
	ReadErr      error
}

// Create creates a database, then reads back the result.
//
// The body arrives already serialized: `resources` cannot import
// `core/state`, which already imports it, so the resolved target is
// translated into a payload by `mapper`, upstream.
//
// The read-back is not overzealous. It brings back the option ids, without
// which the state is blind to drift, and it lets the caller confront the
// actual state with what the plan had announced — on notion-seed's own
// write.
func (r *DatabaseResource) Create(ctx context.Context, body []byte) (CreatedDatabase, error) {
	resp, err := r.tr.Execute(ctx, transport.APIRequest{
		Method: "POST",
		Path:   "/v1/databases",
		Body:   body,
	})
	if err != nil {
		return CreatedDatabase{}, err
	}

	var probe struct {
		ID          string `json:"id"`
		DataSources []struct {
			ID string `json:"id"`
		} `json:"data_sources"`
	}
	if err := json.Unmarshal(resp.Body, &probe); err != nil {
		return CreatedDatabase{}, fmt.Errorf(
			"unreadable response from POST /v1/databases: %w\n"+
				"  → the database may have been created: open the parent page in "+
				"Notion to check before rerunning", err)
	}
	if probe.ID == "" {
		return CreatedDatabase{}, fmt.Errorf(
			"POST /v1/databases returned no identifier\n" +
				"  → the database may have been created: open the parent page in " +
				"Notion to check before rerunning")
	}

	out := CreatedDatabase{ID: probe.ID}
	if len(probe.DataSources) > 0 {
		out.DataSourceID = probe.DataSources[0].ID
	}

	remote, err := r.Read(ctx, probe.ID)
	if err != nil {
		out.ReadErr = err
		return out, nil
	}
	rd, err := asRemoteDatabase(probe.ID, remote)
	if err != nil {
		out.ReadErr = err
		return out, nil
	}
	out.Remote = rd
	if rd.DataSourceID != "" {
		out.DataSourceID = rd.DataSourceID
	}
	return out, nil
}

// UpdatedDatabase holds the result of an update.
//
// DatabaseWritten says whether the database PATCH went through. Without it, a
// failure of the data source PATCH could not say that the name, at least, is
// written — and the user would not know what is left to do.
type UpdatedDatabase struct {
	DatabaseWritten bool
	Remote          RemoteDatabase
	ReadErr         error
}

// Update writes an existing database, then reads back the result.
//
// ORDER: the database first, the data source second. A failure of the second
// then leaves a name and an icon up to date and NO data touched — the
// cheapest failure. The reverse order would write the schema, hence the rows,
// before failing on the cosmetics.
//
// Measured on 2026-09-24, and it is a second reason for this order: on a
// database whose ancestor page is in the trash, the database PATCH returns a
// 400 that NAMES the cause ("archived ancestor"), whereas the data source
// PATCH returns a 404 that blames the sharing with the integration. The order
// therefore lands the user on the right message.
//
// An empty body skips its endpoint: an update that touches only properties
// has nothing to write on the database.
//
// The read-back brings back the ids of new options, without which the state
// is blind to drift, and lets the caller confront the actual state with what
// the plan had announced.
func (r *DatabaseResource) Update(
	ctx context.Context, id, dsID string, dbBody, dsBody []byte,
) (UpdatedDatabase, error) {
	var out UpdatedDatabase

	if len(dbBody) > 0 {
		if _, err := r.tr.Execute(ctx, transport.APIRequest{
			Method: "PATCH",
			Path:   "/v1/databases/" + id,
			Body:   dbBody,
		}); err != nil {
			return out, err
		}
		out.DatabaseWritten = true
	}

	if len(dsBody) > 0 {
		if _, err := r.tr.Execute(ctx, transport.APIRequest{
			Method: "PATCH",
			Path:   "/v1/data_sources/" + dsID,
			Body:   dsBody,
		}); err != nil {
			return out, err
		}
	}

	remote, err := r.Read(ctx, id)
	if err != nil {
		out.ReadErr = err
		return out, nil
	}
	rd, err := asRemoteDatabase(id, remote)
	if err != nil {
		out.ReadErr = err
		return out, nil
	}
	out.Remote = rd
	return out, nil
}

// trashBody is the body that moves a database to the trash. Measured on
// 2026-09-24: it is enough, and the response carries archived=true,
// in_trash=true.
const trashBody = `{"in_trash":true}`

// Trash moves a database to the trash: a single PATCH, on the database.
//
// The boolean says whether the RESPONSE confirms the trashing, according to a
// local rule (archived || in_trash) that MUST stay the same as the decoder's
// (mapper.RemoteDatabaseFromJSON) — the very one by which the next plan would
// classify the database. Duplicating it here, rather than calling the
// decoder, is structural: the decoder also wants the data source body, which
// a trash PATCH never reads back. TestTrashLocalRuleAgreesWithDecoderRule, in
// the mapper package, keeps both rules in sync by running the real decoder. A
// 200 that does not confirm the trashing is not a destruction: the caller
// then keeps the identity, because dropping it would make invisible a
// database that may still exist.
//
// An unreadable response returns an unknown outcome: the call answered 200,
// so the mutation may have happened, and nothing can tell.
func (r *DatabaseResource) Trash(ctx context.Context, id string) (bool, error) {
	resp, err := r.tr.Execute(ctx, transport.APIRequest{
		Method: "PATCH",
		Path:   "/v1/databases/" + id,
		Body:   []byte(trashBody),
	})
	if err != nil {
		return false, err
	}
	var probe struct {
		Archived bool `json:"archived"`
		InTrash  bool `json:"in_trash"`
	}
	if err := json.Unmarshal(resp.Body, &probe); err != nil {
		return false, &transport.OutcomeUnknownError{Cause: fmt.Errorf(
			"unreadable response from PATCH /v1/databases/%s: %w\n"+
				"  → retry; if it persists, check that `ntn` speaks API version "+
				"2025-09-03 (`ntn --version`)", id, err)}
	}
	return probe.Archived || probe.InTrash, nil
}

// asRemoteDatabase brings a database read-back down to its concrete type.
// Read never returns anything else: another type can only come from a
// notion-seed bug, and the message says so rather than letting the user look
// on the Notion side.
func asRemoteDatabase(id string, remote RemoteState) (RemoteDatabase, error) {
	rd, ok := remote.(RemoteDatabase)
	if !ok {
		return RemoteDatabase{}, fmt.Errorf(
			"read-back of %s: unexpected type %T\n"+
				"  → this is a notion-seed bug: report it with the "+
				"output of `notion-seed plan`", id, remote)
	}
	return rd, nil
}

// Diff compares a desired database with its remote state. In MVP 0, the
// remote state is always absent: everything comes out as a creation.
func (r *DatabaseResource) Diff(desired any, remote RemoteState) (Changeset, error) {
	db, ok := desired.(config.Database)
	if !ok {
		return Changeset{}, fmt.Errorf("desired must be a config.Database, got %T", desired)
	}
	return DatabaseChangeset(db, remote), nil
}

// DatabaseChangeset is the diff of a database, without transport or decoder.
//
// It is a package FUNCTION, and the Diff method delegates to it: requiring it
// on a receiver forced the diff engine to build a NewDatabaseResource(nil,
// nil) just to call a pure method — a mine waiting only for a network call to
// be added on this path, and an invariant comment to maintain.
func DatabaseChangeset(db config.Database, remote RemoteState) Changeset {
	cs := Changeset{Resource: "database." + db.Key}

	// The typed nil counts: a non-nil RemoteState whose Exists() is false must
	// lead to a creation just like a nil.
	if remote == nil || !remote.Exists() {
		cs.Kind = KindCreate
		names := make([]string, 0, len(db.Properties))
		for name := range db.Properties {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			cs.Details = append(cs.Details, NewDetail("+",
				fmt.Sprintf("property %q (%s)", name, db.Properties[name].Type),
				change.ClassSafe))
		}
		return cs
	}

	// The fine-grained diff on an existing database arrives in MVP 2, with the
	// refresh and drift detection. Without a state, no resource is matched, so
	// this path is unreachable in MVP 0.
	cs.Kind = KindNone
	return cs
}
