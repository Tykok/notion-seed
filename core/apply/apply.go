// SPDX-License-Identifier: GPL-3.0-or-later

// Package apply executes the plan: it writes to Notion what the plan showed,
// and nothing else.
//
// Scope: creations, updates, destructions — a database removed from the YAML
// is moved to the trash — and the cleanup of state entries whose resource has
// already vanished. Only what the API cannot express is withheld, and named
// with its reason.
package apply

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/tykok/notion-seed/core/diff"
	"github.com/tykok/notion-seed/core/providers/notion/mapper"
	"github.com/tykok/notion-seed/core/providers/notion/resources"
	"github.com/tykok/notion-seed/core/providers/notion/transport"
	"github.com/tykok/notion-seed/core/state"
)

// Creator is what apply needs to write. The interface is declared here, on the
// consumer side, so the tests do not have to build a full transport stack.
type Creator interface {
	Create(ctx context.Context, body []byte) (resources.CreatedDatabase, error)
}

// Updater is what apply needs to write an existing database.
//
// DatabaseExists is part of it, and it is not a convenience: the 404 of the
// data source PATCH blames the sharing with the integration, while the cause
// can be an ancestor page in the trash. Probing the database — and it alone,
// without its data source, which would answer the same 404 — is the only way
// to decide, and it costs a call only on a path that is already failing.
type Updater interface {
	Update(ctx context.Context, id, dsID string, dbBody, dsBody []byte) (resources.UpdatedDatabase, error)
	DatabaseExists(ctx context.Context, id string) (bool, error)
}

// Trasher is what apply needs to move a database to the trash.
//
// A separate interface rather than one more method on Updater: the
// destruction needs neither the two PATCHes nor the existence probe, and each
// test fake thus implements only what it exercises. The boolean says whether
// the response CONFIRMS the trashing.
type Trasher interface {
	Trash(ctx context.Context, id string) (bool, error)
}

// Options holds what comes from the command line and the configuration.
type Options struct {
	Dir          string
	ParentPageID string
	Creator      Creator
	Updater      Updater
	Trasher      Trasher
}

// Report is the account of a run. Each field is a list of lines ready to
// print, in the order they happened.
type Report struct {
	Created   []string
	Updated   []string
	Destroyed []string
	Cleaned   []string
	// Skipped names the withheld resources: Withheld forbids writing them.
	Skipped    []string
	Mismatches []string
}

// Converged says whether the plan was applied in full, and whether the API
// wrote what was announced. It is what decides the exit code: an apply that
// leaves work behind must be loud in CI.
func (r Report) Converged() bool {
	return len(r.Skipped) == 0 && len(r.Mismatches) == 0
}

// Check rejects, BEFORE any write, a plan apply could not walk in full.
//
// The permission to write is Withheld == "", and it alone: a resource whose
// Withheld is empty MUST be written. A creation or an update with no target,
// or a destruction whose identity the state does not hold, can only come from
// a notion-seed bug. Skipping it would make an apply converge that did not
// write what the plan showed; stopping on it would leave written the resources
// before it. So the whole plan is rejected, before the first call.
//
// The command calls it before the confirmation; Run calls it again, because
// the guard must live in the package that writes.
func Check(p *diff.Plan, snap *state.Snapshot) error {
	for _, c := range p.Changes {
		if c.Withheld != "" {
			continue
		}
		switch c.Kind {
		case resources.KindCreate, resources.KindUpdate:
			if c.Target != nil {
				continue
			}
		case resources.KindDestroy:
			if snap != nil && snap.Databases[c.Key].ID != "" {
				continue
			}
		}
		return fmt.Errorf(
			"%s: the plan allows writing it without saying what to write, nothing was "+
				"applied\n"+
				"  → this is a notion-seed bug: report it with the "+
				"output of `notion-seed plan`", c.Resource)
	}
	return nil
}

// checkWriters rejects a plan in which an allowed change has nobody to write
// it. Only a miswired caller gets there: it is a notion-seed bug.
func checkWriters(p *diff.Plan, opts Options) error {
	for _, c := range p.Changes {
		if c.Withheld != "" {
			continue
		}
		var missing string
		switch {
		case c.Kind == resources.KindCreate && opts.Creator == nil:
			missing = "a creation is to be written but no Creator is wired"
		case c.Kind == resources.KindUpdate && opts.Updater == nil:
			missing = "an update is to be written but no Updater is wired"
		case c.Kind == resources.KindDestroy && opts.Trasher == nil:
			missing = "a destruction is to be written but no Trasher is wired"
		default:
			continue
		}
		return fmt.Errorf("%s: %s, nothing was applied\n"+
			"  → this is a notion-seed bug: report it with the "+
			"output of `notion-seed plan`", c.Resource, missing)
	}
	return nil
}

// Run writes the plan's creations, updates and destructions, one resource at a
// time.
//
// The state is saved AFTER EACH successful write, not once at the end: a stop
// at any moment then leaves an exactly true state. What is created is
// anchored, what is not will come out as a creation in the next plan. Saving
// only once at the end would produce, at the slightest interruption, databases
// really created whose existence the state ignores — hence created twice on
// the next apply.
//
// No rollback: archiving what was just created would be a destruction nobody
// asked for.
func Run(ctx context.Context, p *diff.Plan, snap *state.Snapshot, opts Options) (Report, error) {
	var rep Report

	// The guard lives HERE, in the package that writes, and not only in the
	// command. A plan can be blocked by ANOTHER resource than the ones about to
	// be created: without this line, a second caller would write the creations
	// of a rejected plan, and no test would see it.
	if p.Blocked {
		return rep, fmt.Errorf(
			"plan blocked, nothing was applied\n" +
				"  → clear each block listed by `notion-seed plan` before rerunning")
	}

	// Check BEFORE the first write: an allowed change apply could not write
	// stops the whole plan, not half of the series.
	if err := Check(p, snap); err != nil {
		return rep, err
	}
	// Likewise for the writers: a missing Trasher, discovered at the
	// destruction, would leave written the creations before it.
	if err := checkWriters(p, opts); err != nil {
		return rep, err
	}

	if snap.Databases == nil {
		snap.Databases = map[string]state.Database{}
	}

	for _, c := range p.Changes {
		// Withheld IS the ban on writing: see diff.Result.Withheld.
		if c.Withheld != "" {
			rep.Skipped = append(rep.Skipped, c.Resource)
			continue
		}
		// Check guaranteed the shape of each allowed change: a target for a
		// creation or an update, an identity in the state for a destruction.
		var err error
		switch c.Kind {
		case resources.KindCreate:
			err = createOne(ctx, c, &rep, snap, opts)
		case resources.KindUpdate:
			err = updateOne(ctx, c, &rep, snap, opts)
		case resources.KindDestroy:
			err = destroyOne(ctx, c, &rep, snap, opts)
		}
		if err != nil {
			return rep, err
		}
	}

	for _, resource := range p.StaleState {
		key := strings.TrimPrefix(resource, "database.")
		delete(snap.Databases, key)
		if err := state.Save(opts.Dir, snap); err != nil {
			return rep, err
		}
		rep.Cleaned = append(rep.Cleaned, fmt.Sprintf(
			"%s — entry removed from the state, nothing was written to Notion", resource))
	}

	return rep, nil
}

// createOne creates a database and records the read-back result in the state.
func createOne(ctx context.Context, c diff.Change, rep *Report, snap *state.Snapshot, opts Options) error {
	body, err := mapper.DatabaseCreatePayload(c.Key, *c.Target, opts.ParentPageID)
	if err != nil {
		return err
	}

	created, err := opts.Creator.Create(ctx, body)
	if err != nil {
		return creationError(c, *rep, err, opts.ParentPageID)
	}

	if created.ReadErr != nil {
		// The database EXISTS and its id is known. A partial entry is recorded
		// rather than losing the identity: a lost identity costs a duplicate, a
		// partial entry only costs undetectable drift on the options —
		// driftLines ignores options without an id, so no FALSE drift will come
		// out of it.
		partial := *c.Target
		partial.ID = created.ID
		partial.DataSourceID = created.DataSourceID
		snap.Databases[c.Key] = partial
		if serr := state.Save(opts.Dir, snap); serr != nil {
			return serr
		}
		return fmt.Errorf(
			"%s was created (id %s) but its state could not be read back: %w\n"+
				"  → its identity is recorded in %s, so it will not be created again. "+
				"To resync its snapshot, remove its entry from %s then "+
				"run `notion-seed import %s <url>`",
			c.Resource, created.ID, created.ReadErr,
			state.FileName, state.FileName, c.Resource)
	}

	adopted, _ := state.JoinOptionKeys(state.FromRemote(created.Remote), *c.Target)
	snap.Databases[c.Key] = adopted
	if err := state.Save(opts.Dir, snap); err != nil {
		return err
	}
	rep.Created = append(rep.Created, fmt.Sprintf(
		"%s created — id %s (state updated)", c.Resource, created.ID))
	rep.Mismatches = append(rep.Mismatches, mismatchLines(c, adopted)...)
	return nil
}

// mismatchLines confronts the read-back actual state with what the plan had
// announced, on notion-seed's own write.
//
// The plan's comparator is reused as is: `adopted` plays both the "last
// applied state" way and the "actual" way, so any detail that remains is a
// mismatch between the target and what the API really wrote. A second
// comparator written for the occasion would be exactly the kind of parallel
// path this PR exists to remove.
func mismatchLines(c diff.Change, adopted state.Database) []string {
	gap := diff.CompareDatabase(c.Key, c.Target, &adopted, &adopted)
	out := make([]string, 0, len(gap.Changeset.Details))
	for _, d := range gap.Changeset.Details {
		// The details' Note is written for the PLAN context — "absent from the
		// YAML", for example — and reads backwards here: what was missing from
		// the YAML is what the API wrote in excess. So it is not reused, and the
		// real meaning is stated from the operation.
		out = append(out, fmt.Sprintf("%s — %s: %s",
			c.Resource, mismatchVerb(d.Op), d.Target))
	}
	return out
}

// mismatchVerb translates the plan's operation into what really happened
// during the write. `-` is the case that read backwards: in a plan it means
// "to remove", here it means "the API wrote it while the target did not hold
// it".
func mismatchVerb(op string) string {
	switch op {
	case "-":
		return "the API wrote in excess"
	case "~":
		return "the API wrote a different value than the one announced"
	default:
		return "the API did not write"
	}
}

// creationError reports a creation failure, naming what is done and what is
// not. A message that does not say where the series stopped leaves the user
// guessing the state of their workspace.
func creationError(c diff.Change, rep Report, err error, parentPageID string) error {
	acquired := acquiredBefore(rep)

	var unknown *transport.OutcomeUnknownError
	if errors.As(err, &unknown) {
		// We don't know whether the mutation was applied server-side. Above all,
		// nothing is chained: the workspace is in an undetermined state, and the
		// next creation would work blind.
		return fmt.Errorf(
			"creating %s: unknown outcome: %w\n"+
				"  → open the parent page %s in Notion. If the database %q exists, "+
				"adopt it with `notion-seed import %s <url>`; otherwise rerun apply. "+
				"%s",
			c.Resource, err, parentPageID, c.Target.Name, c.Resource, acquired)
	}
	return fmt.Errorf(
		"failed to create %s: %w\n"+
			"  → fix the cause above, then rerun apply; nothing is rolled back, "+
			"%s",
		c.Resource, err, acquired)
}

// acquiredBefore names what the run already wrote and recorded in the state
// before the failing resource. A message that does not say where the series
// stopped leaves the user guessing the state of their workspace.
func acquiredBefore(rep Report) string {
	var parts []string
	if names := resourceNames(rep.Created); len(names) > 0 {
		parts = append(parts, "already created and recorded in the state: "+strings.Join(names, ", "))
	}
	if names := resourceNames(rep.Updated); len(names) > 0 {
		parts = append(parts, "already updated and recorded in the state: "+strings.Join(names, ", "))
	}
	if names := resourceNames(rep.Destroyed); len(names) > 0 {
		parts = append(parts, "already moved to the trash and removed from the state: "+strings.Join(names, ", "))
	}
	if len(parts) == 0 {
		return "no write had succeeded before this one"
	}
	return strings.Join(parts, "; ")
}

// resourceNames extracts the resource name at the head of each report line.
func resourceNames(lines []string) []string {
	names := make([]string, 0, len(lines))
	for _, line := range lines {
		names = append(names, strings.SplitN(line, " ", 2)[0])
	}
	return names
}

// writeSet derives from the PLAN what goes to the API: the database fields and
// the properties that carry at least one line.
//
// It is the product's invariant made literal — what is written is exactly what
// is shown. A property declared but identical to the actual state carries no
// line, so it is not sent; an unmanaged property never carries one.
func writeSet(details []resources.Detail) (fields, props []string) {
	seenF := map[string]bool{}
	seenP := map[string]bool{}
	for _, d := range details {
		if d.Field != "" {
			if !seenF[d.Field] {
				seenF[d.Field] = true
				fields = append(fields, d.Field)
			}
			continue
		}
		if d.Property != "" && !seenP[d.Property] {
			seenP[d.Property] = true
			props = append(props, d.Property)
		}
	}
	sort.Strings(fields)
	sort.Strings(props)
	return fields, props
}

// updateOne writes an existing database and records the read-back result in
// the state.
func updateOne(ctx context.Context, c diff.Change, rep *Report, snap *state.Snapshot, opts Options) error {
	if opts.Updater == nil {
		return fmt.Errorf(
			"%s: an update is to be written but no Updater is wired\n"+
				"  → this is a notion-seed bug: report it with the "+
				"output of `notion-seed plan`", c.Resource)
	}
	// An update's target ALWAYS comes from a fresh read-back: an empty id can
	// only come from an upstream bug. PATCHing "/v1/data_sources/" would point
	// at nothing — it is rejected BEFORE any call.
	if c.Target.ID == "" || c.Target.DataSourceID == "" {
		return fmt.Errorf(
			"%s: the update target carries no database or "+
				"data source id, nothing was written\n"+
				"  → this is a notion-seed bug: report it with the "+
				"output of `notion-seed plan`", c.Resource)
	}

	fields, props := writeSet(c.Details)

	var dbBody, dsBody []byte
	if len(fields) > 0 {
		b, err := mapper.DatabaseUpdatePayload(c.Key, *c.Target, fields)
		if err != nil {
			return err
		}
		dbBody = b
	}
	if len(props) > 0 {
		b, err := mapper.DataSourceUpdatePayload(c.Key, *c.Target, props)
		if err != nil {
			return err
		}
		dsBody = b
	}
	if dbBody == nil && dsBody == nil {
		// A change with no writable line does not exist: the plan produces a
		// KindUpdate only if it carries details. Skipping silently would hide a
		// writeSet bug.
		return fmt.Errorf(
			"%s: the plan announces an update but no write follows from it\n"+
				"  → this is a notion-seed bug: report it with the "+
				"output of `notion-seed plan`", c.Resource)
	}

	upd, err := opts.Updater.Update(ctx, c.Target.ID, c.Target.DataSourceID, dbBody, dsBody)
	if err != nil {
		// The database PATCH went through before the failure: Notion already
		// holds these fields. Recording them keeps the state exactly true —
		// otherwise notion-seed's own write would come out in the next plan as
		// drift from elsewhere.
		if upd.DatabaseWritten {
			snap.Databases[c.Key] = overlayWritten(snap.Databases[c.Key], *c.Target, fields, nil)
			if serr := state.Save(opts.Dir, snap); serr != nil {
				return serr
			}
		}
		return updateError(ctx, c, *rep, err, upd, fields, len(dbBody) > 0, opts)
	}
	if upd.ReadErr != nil {
		// Both writes went through, only the read-back is missing: what was
		// WRITTEN is recorded, for lack of being able to record what was read
		// back.
		snap.Databases[c.Key] = overlayWritten(snap.Databases[c.Key], *c.Target, fields, props)
		if serr := state.Save(opts.Dir, snap); serr != nil {
			return serr
		}
		return fmt.Errorf(
			"%s was updated but its state could not be read back: %w\n"+
				"  → its entry in %s holds what was written, without the ids of the new "+
				"options that only the read-back brings. Run `notion-seed plan` again to "+
				"see the actual state",
			c.Resource, upd.ReadErr, state.FileName)
	}

	adopted, _ := state.JoinOptionKeys(state.FromRemote(upd.Remote), *c.Target)
	snap.Databases[c.Key] = adopted
	if err := state.Save(opts.Dir, snap); err != nil {
		return err
	}
	rep.Updated = append(rep.Updated, fmt.Sprintf(
		"%s updated — %d field(s), %d property(ies) (state updated)",
		c.Resource, len(fields), len(props)))
	rep.Mismatches = append(rep.Mismatches, mismatchLines(c, adopted)...)
	return nil
}

// overlayWritten lays over the prior state entry EXACTLY what was written: the
// database fields of the write set and, if the data source was written, the
// properties of the set, taken from the target.
//
// The target is not recorded as a whole: it holds only the actual state read
// at plan time and the declared one, and would overwrite what else the state
// knows. The option keys come from the target itself, which already holds
// them: JoinOptionKeys, which starts from a read-back actual state, has
// nothing to add here.
func overlayWritten(prior, target state.Database, fields, props []string) state.Database {
	out := prior
	out.ID, out.DataSourceID = target.ID, target.DataSourceID
	for _, f := range fields {
		switch f {
		case "name":
			out.Name = target.Name
		case "description":
			out.Description = target.Description
		case "icon":
			out.Icon = target.Icon
		}
	}
	// Copy: the prior entry shares its map with the snapshot.
	out.Properties = make(map[string]state.Property, len(prior.Properties)+len(props))
	for name, prop := range prior.Properties {
		out.Properties[name] = prop
	}
	for _, name := range props {
		if prop, ok := target.Properties[name]; ok {
			out.Properties[name] = prop
		} else {
			delete(out.Properties, name)
		}
	}
	return out
}

// fieldLabels names, in plain words, the database fields written.
func fieldLabels(fields []string) string {
	labels := make([]string, 0, len(fields))
	for _, f := range fields {
		switch f {
		case "name":
			labels = append(labels, "the name")
		case "description":
			labels = append(labels, "the description")
		case "icon":
			labels = append(labels, "the icon")
		default:
			labels = append(labels, f)
		}
	}
	if len(labels) <= 1 {
		return strings.Join(labels, "")
	}
	return strings.Join(labels[:len(labels)-1], ", ") + " and " + labels[len(labels)-1]
}

// updateError reports an update failure, naming what went through on this
// resource, and what was done before it.
//
// Which PATCH failed is deduced: if a body was going to the database and it is
// not written, it is the first one; otherwise, it is the data source.
func updateError(
	ctx context.Context, c diff.Change, rep Report, err error,
	upd resources.UpdatedDatabase, fields []string, dbSent bool, opts Options,
) error {
	dsFailed := upd.DatabaseWritten || !dbSent
	acquired := acquiredBefore(rep)
	here := "nothing was written on this resource"
	if upd.DatabaseWritten {
		here = "already written on this resource: " + fieldLabels(fields) +
			"; no row data was touched"
	}

	var unknown *transport.OutcomeUnknownError
	if errors.As(err, &unknown) {
		// Hard stop, nothing chained: the next resource would work on a workspace
		// in an undetermined state. Unlike creation, no identity is at stake —
		// the plan's read-back is enough.
		pending := "the write may have succeeded server-side"
		if upd.DatabaseWritten {
			pending = here + ", but the schema write may have succeeded server-side"
		}
		return fmt.Errorf(
			"updating %s: unknown outcome: %w\n"+
				"  → run `notion-seed plan` to see what Notion actually holds; "+
				"that is enough, nothing needs re-adopting. %s. %s",
			c.Resource, err, pending, acquired)
	}

	var apiErr *transport.APIError
	if !errors.As(err, &apiErr) {
		return genericUpdateError(c, err, here, acquired, upd.DatabaseWritten)
	}

	// Measured on 2026-09-24: under an ancestor page in the trash, the database
	// PATCH returns a 400 that names the cause.
	if !dsFailed && apiErr.Status == 400 && strings.Contains(apiErr.Message, "archived ancestor") {
		return fmt.Errorf(
			"failed to update %s: an ancestor page of the database is in "+
				"the trash\n"+
				"  → restore the parent page in Notion, then rerun apply. %s; %s",
			c.Resource, here, acquired)
	}

	// The 404 of the data source PATCH blames the sharing with the
	// integration, and it can lie: measured on 2026-09-24, an ancestor page in
	// the trash produces exactly this 404, while GET /v1/databases returns 200.
	// Its text is therefore NEVER relayed as is; the database probe decides.
	if dsFailed && apiErr.Status == 404 {
		exists, perr := opts.Updater.DatabaseExists(ctx, c.Target.ID)
		switch {
		case perr != nil:
			return fmt.Errorf(
				"failed to update %s: the data source returns %d %s, and the "+
					"database could not be read back to find the cause: %w\n"+
					"  → fix the cause above, then rerun apply; nothing is "+
					"rolled back. %s; %s",
				c.Resource, apiErr.Status, apiErr.NotionCode, perr, here, acquired)
		case exists && upd.DatabaseWritten:
			// The database PATCH just went through: an ancestor in the trash would
			// have failed it first (measured, see above). This diagnosis is
			// therefore ruled out, and only the data source remains at fault.
			return fmt.Errorf(
				"failed to update %s: the database can be read and was just "+
					"written, but its data source returns %d — it vanished, or is no longer "+
					"shared with the integration\n"+
					"  → check in Notion that the database is still shared with "+
					"the integration, then run `notion-seed plan` again. %s; %s",
				c.Resource, apiErr.Status, here, acquired)
		case exists:
			return fmt.Errorf(
				"failed to update %s: archived ancestor — the database can still be "+
					"read, but an ancestor page is in the trash\n"+
					"  → restore the parent page in Notion, then rerun apply. %s; %s",
				c.Resource, here, acquired)
		default:
			return fmt.Errorf(
				"failed to update %s: the database really vanished, or "+
					"is no longer shared with the integration\n"+
					"  → check in Notion that it exists and is shared with "+
					"the integration, then run `notion-seed plan` again. %s; %s",
				c.Resource, here, acquired)
		}
	}

	return genericUpdateError(c, err, here, acquired, upd.DatabaseWritten)
}

// genericUpdateError is the message of a failure with no specific diagnosis.
func genericUpdateError(c diff.Change, err error, here, acquired string, dbWritten bool) error {
	next := "fix the cause above, then rerun apply"
	if dbWritten {
		next += "; `notion-seed plan` shows what is left to write"
	}
	return fmt.Errorf(
		"failed to update %s: %w\n"+
			"  → %s; nothing is rolled back. %s; %s",
		c.Resource, err, next, here, acquired)
}

// destroyOne moves a database to the trash, then removes its entry from the
// state.
//
// The identity comes from the STATE, not from a target: a destruction has no
// state afterwards. Check already guaranteed it is there.
//
// The entry is removed only if the API CONFIRMS the trashing. Any other
// outcome keeps it, and the next plan reads the actual state back to decide: a
// database that is gone comes out there as a stale state entry, which apply
// removes without writing anything; a database still there comes out as a
// destruction. Removing the entry on a doubt would abandon the identity of a
// possibly live database, which would become invisible to notion-seed:
// neither declared, nor in the state.
//
// Run does not read Acknowledged: lifecycle.acknowledge_destroy is an
// acknowledgement of the plan, not a ban on writing.
func destroyOne(ctx context.Context, c diff.Change, rep *Report, snap *state.Snapshot, opts Options) error {
	id := snap.Databases[c.Key].ID

	trashed, err := opts.Trasher.Trash(ctx, id)
	if err != nil {
		return destroyError(c, *rep, err)
	}
	if !trashed {
		rep.Mismatches = append(rep.Mismatches, fmt.Sprintf(
			"%s — the API answered without moving the database to the trash: its state "+
				"entry is kept", c.Resource))
		return nil
	}

	delete(snap.Databases, c.Key)
	if err := state.Save(opts.Dir, snap); err != nil {
		// The API already confirmed the trashing: state.Save's %w says "the
		// previous state is intact", which is true for the FILE but would hide
		// that the database itself is already gone. Saying it here avoids the
		// opposite of destroyError: a reader who would think there is nothing
		// to do. `plan` only reads back and classifies, never writes: it is a
		// following `apply`, on its StaleState, that removes the entry.
		return fmt.Errorf(
			"%s was indeed moved to the trash in Notion, but its entry could not "+
				"be removed from %s: %w\n"+
				"  → run `notion-seed plan` again: the database will read there as in the "+
				"trash, its entry will show up as a stale state entry, which a following "+
				"`apply` will remove without writing anything to Notion",
			c.Resource, state.FileName, err)
	}
	rep.Destroyed = append(rep.Destroyed, fmt.Sprintf(
		"%s moved to the trash — id %s (entry removed from the state)", c.Resource, id))
	return nil
}

// destroyError reports a trashing failure. Whatever it is, the state entry is
// KEPT (see destroyOne): the message says so, and names what was done
// before.
func destroyError(c diff.Change, rep Report, err error) error {
	acquired := acquiredBefore(rep)

	var unknown *transport.OutcomeUnknownError
	if errors.As(err, &unknown) {
		// Hard stop, nothing chained: the next resource would work on a workspace
		// in an undetermined state.
		return fmt.Errorf(
			"trashing %s: unknown outcome: %w\n"+
				"  → run `notion-seed plan` to see what Notion actually holds: "+
				"if the database is in the trash, it comes out there as a stale state "+
				"entry, which apply will remove without writing anything; otherwise, its "+
				"destruction is offered again. Its state entry is kept; %s",
			c.Resource, err, acquired)
	}

	var apiErr *transport.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Status == 400 && strings.Contains(apiErr.Message, "archived ancestor"):
			// Measured on 2026-09-25 on {"in_trash":true}: under an ancestor page
			// already in the trash, Notion answers this 400 and changes nothing —
			// the database then reads back as 200, archived:false. Restoring the
			// page therefore makes the destruction writable. Permanently deleting
			// the parent page was not measured here: what reading the database
			// will then return (404, or archived) is not guaranteed, only the next
			// `notion-seed plan` decides — see cli/plan.go, where a 404 and an
			// archived read both count as a stale state entry.
			return fmt.Errorf(
				"failed to trash %s: an ancestor page of the database "+
					"is already in the trash, and Notion refuses to write under it — the "+
					"database is already going there with its parent page\n"+
					"  → either restore the parent page in Notion, then rerun apply; "+
					"or permanently delete the parent page from the Notion "+
					"trash, then run `notion-seed plan` again: if Notion no longer knows the "+
					"database, its entry will show up as a stale state entry, which a "+
					"following `apply` will remove without writing anything. Its state entry is "+
					"kept; %s",
				c.Resource, acquired)
		case apiErr.Status == 404:
			// The plan had just read it. This 404 is no proof of disappearance:
			// only the next plan's refresh decides, by the same rule as for any
			// orphan. No existence probe here: the ancestor-in-the-trash case is
			// measured to answer 400 on this path, no lying 404 is measured there,
			// and the plan reads the actual state back.
			return fmt.Errorf(
				"failed to trash %s: Notion no longer finds the database "+
					"(404), while the plan had just read it\n"+
					"  → run `notion-seed plan`: if it vanished, it comes out there as a "+
					"stale state entry, which apply will remove without writing anything to "+
					"Notion. Its state entry is kept: a 404 on a write is not "+
					"enough to abandon an identity; %s",
				c.Resource, acquired)
		}
	}

	return fmt.Errorf(
		"failed to trash %s: %w\n"+
			"  → fix the cause above, then rerun apply; nothing is rolled back, "+
			"and its state entry is kept; %s",
		c.Resource, err, acquired)
}
