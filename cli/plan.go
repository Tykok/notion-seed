// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/spf13/cobra"
	"github.com/tykok/notion-seed/core/change"
	"github.com/tykok/notion-seed/core/config"
	"github.com/tykok/notion-seed/core/diff"
	"github.com/tykok/notion-seed/core/measure"
	"github.com/tykok/notion-seed/core/preflight"
	"github.com/tykok/notion-seed/core/providers/notion/mapper"
	"github.com/tykok/notion-seed/core/providers/notion/resources"
	"github.com/tykok/notion-seed/core/providers/notion/transport"
	"github.com/tykok/notion-seed/core/state"
)

// planOptions holds the flags shared by plan and diff.
type planOptions struct {
	dir           string
	skipPreflight bool
	ratePerSec    float64
	burst         int
	failOn        []string
}

func (o *planOptions) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&o.dir, "dir", ".",
		"configuration directory (contains workspace.yaml and databases/)")
	cmd.Flags().BoolVar(&o.skipPreflight, "skip-preflight", false,
		"fully offline mode: checks neither ntn (presence, version, auth) "+
			"nor the existence of the parent page. Validates the configuration and renders the "+
			"plan without any network call.")
	cmd.Flags().Float64Var(&o.ratePerSec, "rate", transport.DefaultRatePerSec,
		"cap on API calls per second")
	cmd.Flags().IntVar(&o.burst, "burst", transport.DefaultBurst,
		"number of calls tolerated in a burst")
	cmd.Flags().StringSliceVar(&o.failOn, "fail-on", nil,
		"change classes that cause a non-zero exit code: destructive, "+
			"silent-rewrite, unknown, migration. Empty = nothing fails.")
}

// validate rejects the flag values the rate limiter refuses. Without it,
// `--rate 0` would surface as a panic from NewTokenBucket, which is a useless
// message for the user.
func (o *planOptions) validate() error {
	if o.ratePerSec <= 0 {
		return fmt.Errorf("--rate must be strictly positive, got %v", o.ratePerSec)
	}
	if o.burst <= 0 {
		return fmt.Errorf("--burst must be strictly positive, got %d", o.burst)
	}
	// The --fail-on value is resolved HERE, hence before config.Load and before
	// any network call. A mistyped name rejected only after the plan would let a
	// CI go green believing it is protected: the flag would not protect, and
	// nobody would know.
	if _, err := failOnClasses(o.failOn); err != nil {
		return err
	}
	return nil
}

// failOnClasses translates the --fail-on values into classes.
//
// The list is ENUMERATED, not a threshold: `safe`, `destructive` and `silent
// rewrite` do form a scale, but an unknown impact has no place on it — an
// unmeasured type pair can turn out harmless or catastrophic. Treating it as
// "worse than destructive" would be as wrong as the opposite.
func failOnClasses(values []string) ([]change.Class, error) {
	byName := map[string]change.Class{
		"destructive":    change.ClassDestructive,
		"silent-rewrite": change.ClassSilentRewrite,
		"unknown":        change.ClassUnknownImpact,
		"migration":      change.ClassMigration,
	}
	out := make([]change.Class, 0, len(values))
	for _, v := range values {
		c, ok := byName[v]
		if !ok {
			return nil, fmt.Errorf(
				"--fail-on does not know %q\n"+
					"  → accepted values: destructive, silent-rewrite, unknown, "+
					"migration; separate them with commas", v)
		}
		out = append(out, c)
	}
	return out, nil
}

// firstMatchingClass returns the first class of the plan that is in the list,
// or ClassSafe if none matches.
//
// The search looks at the DETAILS, not at the resource's aggregate class: the
// detail is what carries the measured cost, and the aggregate of an otherwise
// dangerous resource would fail on a line that costs nothing.
func firstMatchingClass(p *diff.Plan, classes []change.Class) change.Class {
	for _, c := range p.Changes {
		for _, d := range c.Details {
			for _, want := range classes {
				if d.Class == want {
					return want
				}
			}
		}
	}
	return change.ClassSafe
}

// checkFailOn exits with a non-zero code if the plan carries one of the
// classes enumerated by --fail-on.
//
// plan, diff AND apply go through here: the three commands share planOptions,
// so all three expose the flag. A flag shown in apply's help but ignored by
// apply would be worse than no flag at all — the CI that writes is precisely
// the one that believes it is protected.
func checkFailOn(p *diff.Plan, failOn []string) error {
	classes, err := failOnClasses(failOn)
	if err != nil {
		return err
	}
	if hit := firstMatchingClass(p, classes); hit != change.ClassSafe {
		return fmt.Errorf(
			"the plan carries a change of class %q, rejected by --fail-on\n"+
				"  → review the plan above; remove this class from --fail-on "+
				"if the change is intended", hit)
	}
	return nil
}

func newPlanCmd() *cobra.Command {
	opts := &planOptions{}
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Compute and show the changes, without applying anything",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPlan(cmd, opts)
		},
	}
	opts.bind(cmd)
	return cmd
}

// prepared holds everything plan and apply compute the same way.
//
// Both commands MUST go through here: if apply recomputed differently, the
// plan shown and what is written could diverge — the very defect the resolved
// target exists to make impossible.
type prepared struct {
	cfg  *config.Config
	snap *state.Snapshot
	plan *diff.Plan
	// tr is nil under --skip-preflight: no call was made.
	tr transport.Transport
	// workspaceID is the one ntn is authenticated on. Empty under
	// --skip-preflight, where no whoami was made. apply records it in the state
	// it creates: without it, checkWorkspaceMatch stays disarmed forever for a
	// project bootstrapped by apply rather than by import.
	workspaceID string
	// measureFailures holds the counts that did not succeed. They are
	// incidents, not plan: they are printed on stderr, and fail neither plan
	// nor apply — the affected line simply stays "unknown impact", which is the
	// truth.
	measureFailures []string
}

func preparePlan(cmd *cobra.Command, opts *planOptions) (*prepared, error) {
	// No fallback on a nil context: cobra always populates it before RunE
	// (checked in the v1.10.2 sources), and a silent fallback would hide a
	// programming error instead of revealing it.
	ctx := cmd.Context()

	// 0. Validate the flags BEFORE anything. NewTokenBucket panics on a
	// non-positive rate, and a panic is a useless message for someone who typed
	// `--rate 0`.
	if err := opts.validate(); err != nil {
		return nil, err
	}

	// 1. Load — validate each file, merge, THEN check the global uniqueness of
	// keys. config.Load guarantees this order.
	cfg, err := config.Load(opts.dir)
	if err != nil {
		return nil, err
	}

	// 2. State — the last applied state. Absent = first run, everything comes
	// out as a creation, exactly as before the state existed.
	snap, err := state.Load(opts.dir)
	if err != nil {
		return nil, err
	}

	out := &prepared{cfg: cfg, snap: snap}
	var refreshed map[string]diff.Refreshed
	if !opts.skipPreflight {
		info, err := preflight.Check(ctx, "ntn")
		if err != nil {
			return nil, err
		}
		cmd.Printf("ntn %s — workspace %s (%s)\n\n",
			info.NtnVersion, info.WorkspaceName, info.WorkspaceID)

		if err := checkWorkspaceMatch(snap, info.WorkspaceID); err != nil {
			return nil, err
		}
		out.workspaceID = info.WorkspaceID

		out.tr = newTransport(cmd, opts)
		if err := checkParentPage(ctx, out.tr, cfg.Workspace.ParentPageID, opts.ratePerSec); err != nil {
			return nil, err
		}

		// 3. Refresh — read the actual state of only the resources the state
		// anchors. Without an id, no matching is possible: a database that was
		// not imported comes out as a creation, which is accurate.
		refreshed, err = refreshManaged(ctx, out.tr, snap)
		if err != nil {
			return nil, err
		}
	}

	// 4. Diff — config, state and actual state.
	out.plan, err = diff.Compute(cfg, snap, refreshed)
	if err != nil {
		return nil, err
	}

	// 5. Measurement — fill in the counts and reclassify. Offline, nothing is
	// measured and each affected line stays "unknown impact", which is the
	// truth: nothing was read.
	if out.tr != nil {
		out.measureFailures = measure.Enrich(ctx, measure.NewCounter(out.tr),
			measuredDataSourceIDs(snap, refreshed), out.plan)
	}
	return out, nil
}

// measuredDataSourceIDs maps each key to the data source to QUERY.
//
// The fresh id, the one the refresh just read, wins over the state's: the plan
// is computed against the actual state read back, and measuring elsewhere
// would count the rows of another object. A data source whose id changed
// leaves behind an old id that may still be alive — it then belongs to another
// database, answers 200, and the 0 coming out of it would announce "nothing
// to lose" on a removal that costs.
//
// The state remains the fallback: under --skip-preflight nothing is read back,
// and a resource read back without a data source id has nothing better to
// offer.
func measuredDataSourceIDs(snap *state.Snapshot, refreshed map[string]diff.Refreshed) map[string]string {
	out := make(map[string]string, len(refreshed))
	if snap != nil {
		for key, db := range snap.Databases {
			out[key] = db.DataSourceID
		}
	}
	for key, r := range refreshed {
		// A resource that is not found or archived was not read: its Database
		// is empty, and overwriting with empty would erase the fallback.
		if r.Missing || r.Database.DataSourceID == "" {
			continue
		}
		out[key] = r.Database.DataSourceID
	}
	return out
}

// reportMeasureFailures says what could not be counted, on stderr: these are
// incidents, not plan. stdout carries only the plan, which must stay usable in
// a pipe.
func reportMeasureFailures(cmd *cobra.Command, prep *prepared) {
	for _, f := range prep.measureFailures {
		fmt.Fprintln(cmd.ErrOrStderr(), "count failed — "+f)
	}
}

func runPlan(cmd *cobra.Command, opts *planOptions) error {
	prep, err := preparePlan(cmd, opts)
	if err != nil {
		return err
	}
	p := prep.plan
	reportMeasureFailures(cmd, prep)

	// 6. Render — plain text on stdout.
	if err := diff.Render(cmd.OutOrStdout(), p); err != nil {
		return err
	}
	if p.Blocked {
		// The render above already details each block reason with its
		// corrective action ("Plan blocked" section). This error does not
		// repeat them, but still carries its own arrow: it reaches the user as
		// is on stderr (cli/root.go prints it), and the project's constraint on
		// error messages is unconditional.
		return fmt.Errorf("plan blocked\n" +
			"  → each block is detailed above, with what clears it")
	}
	// AFTER the render: --fail-on exits with a non-zero code, it does not
	// deprive the user of the plan that explains why. The error message points
	// to what was just printed.
	return checkFailOn(p, opts.failOn)
}

// checkWorkspaceMatch rejects a state coming from another workspace.
//
// An empty workspace_id is NOT a disagreement: it is a state written before the
// field existed, or by hand. It cannot be checked, so notion-seed does not
// pretend to know.
func checkWorkspaceMatch(snap *state.Snapshot, workspaceID string) error {
	if snap == nil || snap.WorkspaceID == "" || snap.WorkspaceID == workspaceID {
		return nil
	}
	return fmt.Errorf(
		"%s describes workspace %s, but ntn is authenticated on %s\n"+
			"  → the state's identities do not exist in this workspace. Authenticate "+
			"ntn on the right workspace (`ntn login`), or work in the matching "+
			"configuration directory",
		state.FileName, snap.WorkspaceID, workspaceID)
}

// refreshManaged reads the actual state of each resource anchored by the state.
//
// A resource that is not found or archived is not a command error: it is a
// fact the plan must report and will block on. Surfacing it as an error would
// deprive the user of the rest of the plan.
func refreshManaged(ctx context.Context, tr transport.Transport, snap *state.Snapshot) (map[string]diff.Refreshed, error) {
	if snap == nil || len(snap.Databases) == 0 {
		return nil, nil
	}
	res := resources.NewDatabaseResource(tr, mapper.RemoteDatabaseFromJSON)
	out := make(map[string]diff.Refreshed, len(snap.Databases))

	keys := make([]string, 0, len(snap.Databases))
	for key := range snap.Databases {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		if snap.Databases[key].ID == "" {
			return nil, fmt.Errorf(
				"database.%s has no identifier in %s\n"+
					"  → the entry is incomplete: restore the file from git, or "+
					"remove this entry and re-import the database",
				key, state.FileName)
		}
		remote, err := res.Read(ctx, snap.Databases[key].ID)
		if err != nil {
			var apiErr *transport.APIError
			if errors.As(err, &apiErr) && apiErr.Status == 404 {
				out[key] = diff.Refreshed{Missing: true, Reason: "not found (404)"}
				continue
			}
			return nil, fmt.Errorf(
				"failed to read database.%s (id %s): %w\n"+
					"  → retry; if the database was deleted, remove its entry from %s",
				key, snap.Databases[key].ID, err, state.FileName)
		}
		rd, ok := remote.(resources.RemoteDatabase)
		if !ok || !rd.Found {
			out[key] = diff.Refreshed{Missing: true, Reason: "not found"}
			continue
		}
		if rd.Archived {
			out[key] = diff.Refreshed{Missing: true, Reason: "archived or in the trash"}
			continue
		}
		out[key] = diff.Refreshed{Database: state.FromRemote(rd), DataSources: rd.DataSourceCount}
	}
	return out, nil
}

// newTransport assembles the stack: shell-out to ntn, wrapped in the shared
// rate limiter and the retry policy.
//
// Retry waits go to stderr, not stdout: stdout carries only the plan, which
// must stay identical between two runs and usable in a pipe.
func newTransport(cmd *cobra.Command, opts *planOptions) transport.Transport {
	clock := transport.RealClock{}
	return transport.NewRetrying(
		transport.NewNtnShell(),
		transport.NewTokenBucket(opts.ratePerSec, opts.burst, clock),
		transport.DefaultRetryPolicy(),
		clock,
		func(msg string) { fmt.Fprintln(cmd.ErrOrStderr(), msg) },
	)
}

// checkParentPage checks that the parent page is readable. Since ntn's token
// is user-scoped, it sees the whole workspace: a 404 here means the page does
// not exist, not that it was not shared.
func checkParentPage(ctx context.Context, tr transport.Transport, pageID string, ratePerSec float64) error {
	// The schema currently requires a non-empty parent_page_id matching a UUID
	// (pattern): this branch is therefore unreachable in practice as long as
	// the field stays required. The safeguard stays useful for the day
	// parent_page_id becomes optional, when pages become resources (post-MVP).
	if pageID == "" {
		return fmt.Errorf("workspace.parent_page_id is empty in workspace.yaml")
	}
	resp, err := tr.Execute(ctx, transport.APIRequest{
		Method: "GET",
		Path:   "/v1/pages/" + pageID,
	})
	if err == nil {
		return checkParentPageAlive(pageID, resp.Body)
	}

	// An unknown outcome is not a failure: the type says so itself. Reporting
	// it as "unreadable page" would assert what is not known.
	var unknown *transport.OutcomeUnknownError
	if errors.As(err, &unknown) {
		return fmt.Errorf(
			"cannot tell whether parent page %s is readable: %w\n"+
				"  → rerun the command; if it persists, increase the timeout",
			pageID, err)
	}

	// The "the page does not exist" reasoning holds ONLY for a 404. ntn's
	// token is user-scoped and sees the whole workspace without prior sharing,
	// so a 404 cannot come from a sharing defect — but attaching this reasoning
	// to a 400 (the schema already validates the format of parent_page_id; a
	// 400 therefore means something else) or to a 403 would send the user down
	// a false trail.
	var apiErr *transport.APIError
	errors.As(err, &apiErr)
	if apiErr != nil && apiErr.Status == 404 {
		return fmt.Errorf(
			"parent page %s not found: %w\n"+
				"  → this page does not exist. ntn's token sees the whole workspace "+
				"without prior sharing, so this is not a permissions problem — "+
				"check workspace.parent_page_id",
			pageID, err)
	}

	// An exhausted 429 is the most likely non-404 failure, and it is the only
	// one whose corrective action is a setting of notion-seed itself.
	if apiErr != nil && apiErr.Status == 429 {
		return fmt.Errorf(
			"parent page %s unreadable: %w\n"+
				"  → the API rate-limited and the attempts are exhausted: "+
				"retry in a minute, or lower `--rate` (currently %v calls/s)",
			pageID, err, ratePerSec)
	}

	if apiErr == nil {
		// The error does not come from the API: unrecognized ntn output format,
		// binary not found, malformed call. It already carries its own
		// corrective action — stacking a second generic hint would send the
		// user looking in the wrong place.
		return fmt.Errorf("parent page %s unreadable: %w", pageID, err)
	}

	return fmt.Errorf(
		"parent page %s unreadable: %w\n"+
			"  → check that `ntn` is authenticated on the right workspace "+
			"(`notion-seed init`) and that workspace.parent_page_id points to a page "+
			"of this workspace; retry if the API has an incident",
		pageID, err)
}

// checkParentPageAlive rejects a parent page in the trash.
//
// A page in the trash reads as 200: the status code says nothing, only its
// in_trash field does. Measured on 2026-09-25 against API 2025-09-03: a page
// carries in_trash (true in the trash, false otherwise) and no archived field.
// archived is still read, out of caution towards a response from another API
// version, but it is not what detects. Without this check, plan announced
// "No changes" while every write under the page is refused (`400
// validation_error — Can't edit page on block with an archived ancestor`).
//
// An ancestor in the trash is detected too: measured on 2026-09-25, a page
// whose parent page, or a page two levels up, is in the trash reads
// in_trash:true, without having been written. A database, however, does not
// inherit this field: under the same ancestor, it reads in_trash:false. That
// is why the check is on the parent page, not on the databases.
//
// A response that carries NEITHER field is rejected rather than read as
// "live": the API's silence is not a measurement, and that is exactly the
// assertion this check exists to stop making.
func checkParentPageAlive(pageID string, body []byte) error {
	var page struct {
		Archived *bool `json:"archived"`
		InTrash  *bool `json:"in_trash"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return fmt.Errorf(
			"cannot tell whether parent page %s is in the trash: "+
				"unreadable response from the API: %v\n"+
				"  → retry; if it persists, %s",
			pageID, err, preflight.PinNtnHint())
	}
	if page.Archived == nil && page.InTrash == nil {
		return fmt.Errorf(
			"cannot tell whether parent page %s is in the trash: "+
				"the API response carries neither archived nor in_trash\n"+
				"  → retry; if it persists, %s",
			pageID, preflight.PinNtnHint())
	}
	if (page.Archived != nil && *page.Archived) || (page.InTrash != nil && *page.InTrash) {
		return fmt.Errorf(
			"parent page %s is in the trash: Notion refuses to write under it\n"+
				"  → restore it from the Notion trash, or point "+
				"workspace.parent_page_id to a live page",
			pageID)
	}
	return nil
}
