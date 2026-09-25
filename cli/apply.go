// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tykok/notion-seed/core/apply"
	"github.com/tykok/notion-seed/core/diff"
	"github.com/tykok/notion-seed/core/planfile"
	"github.com/tykok/notion-seed/core/providers/notion/mapper"
	"github.com/tykok/notion-seed/core/providers/notion/resources"
)

// confirmWord is the word to type. In full, not just Enter: a confirmation
// given without reading protects against nothing.
const confirmWord = "apply"

// isInteractive says whether standard input is a terminal.
//
// It is a variable so the tests can force it: a character device cannot be
// simulated in `go test`, and adding a production flag for that would ship a
// bypass in the released binary.
//
// /dev/null is excluded by name: it is a character device, like a terminal,
// and the mode test cannot tell them apart. Without this exclusion, an input
// wired to /dev/null would pass for a terminal, and its immediate end of input
// for a Ctrl-D — confirm would then say "confirmation interrupted" to someone
// who never had a terminal, instead of asking for --auto-approve.
var isInteractive = func(cmd *cobra.Command) bool {
	f, ok := cmd.InOrStdin().(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(info, null) {
		return false
	}
	return true
}

func newApplyCmd() *cobra.Command {
	opts := &planOptions{}
	autoApprove := false

	cmd := &cobra.Command{
		Use:   "apply [plan-file]",
		Short: "Apply the plan: create, update and trash databases, and update the state",
		Long: "apply recomputes the plan, prints it, asks for confirmation, then writes.\n\n" +
			"Given a file written by `plan --out`, apply still recomputes the plan —\n" +
			"nothing is replayed — and holds it to the reviewed one BEFORE printing\n" +
			"it: same resources, same lines, no count higher and no bound vaguer\n" +
			"than reviewed, same notion-seed version, same workspace, configuration\n" +
			"and state. Any difference refuses the apply, named line by line, and\n" +
			"nothing is written. Without a file, nothing of this applies.\n\n" +
			"apply writes creations, updates, destructions — a database the YAML\n" +
			"no longer declares is moved to the Notion trash — and the cleanup of\n" +
			"state entries whose resource has already vanished.\n" +
			"An update the API cannot express — renaming an option, changing its\n" +
			"color — is named under \"Withheld\", with the migration to do by hand,\n" +
			"and apply exits with a non-zero code as long as it remains.\n\n" +
			"Confirmation clears no block: a plan blocked by a managed resource\n" +
			"Notion no longer knows never reaches the prompt.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			planPath := ""
			if len(args) == 1 {
				planPath = args[0]
			}
			return runApply(cmd, opts, autoApprove, planPath)
		},
	}
	opts.bind(cmd)
	cmd.Flags().BoolVar(&autoApprove, "auto-approve", false,
		"apply without asking for confirmation. Required outside a terminal (CI).")
	// Writing offline makes no sense: the flag is hidden from the help AND
	// rejected if passed — a flag accepted then ignored would suggest an offline
	// apply that actually called the API.
	_ = cmd.Flags().MarkHidden("skip-preflight")
	return cmd
}

// runApply applies the recomputed plan. planPath is the reviewed plan file,
// "" for none.
func runApply(cmd *cobra.Command, opts *planOptions, autoApprove bool, planPath string) error {
	if cmd.Flags().Changed("skip-preflight") {
		return fmt.Errorf(
			"apply does not accept --skip-preflight\n" +
				"  → the command writes to Notion after reading the actual state: it has " +
				"nothing to do offline. Use `notion-seed plan --skip-preflight` " +
				"to validate the configuration without network")
	}

	// The plan file is read BEFORE anything else: a missing file, an
	// unreadable one, an unknown format or another version is known without
	// the network, and refused without spending a single call.
	var reviewed *planfile.File
	if planPath != "" {
		saved, err := readReviewedPlan(planPath)
		if err != nil {
			return err
		}
		reviewed = &saved
	}

	prep, err := preparePlan(cmd, opts)
	if err != nil {
		return err
	}
	reportMeasureFailures(cmd, prep)

	// The reviewed plan, BEFORE the render, the confirmation and any write:
	// a plan that is no longer the reviewed one is not shown as if it were
	// about to go out. Once it passes, the RECOMPUTED plan is rendered — its
	// counts are the ones that will go out, and they can only be lower or
	// equal.
	//
	// It runs before prep.snap.WorkspaceID is stamped, below: the state's
	// fingerprint is the file's, as at plan time.
	if reviewed != nil {
		if err := checkReviewedPlan(planPath, *reviewed, prep); err != nil {
			return err
		}
	}

	out := cmd.OutOrStdout()
	// The aggregate line counts only what apply will write: a withheld resource
	// keeps an impact apply will not cause, and announcing it here would make it
	// lie right before the confirmation.
	if err := diff.RenderForApply(out, prep.plan); err != nil {
		return err
	}

	todo := splitByWritability(prep.plan)
	if err := renderWithheld(out, todo.withheld); err != nil {
		return err
	}

	// The block wins over everything, --auto-approve included. Consent is
	// declarative: it is given in workspace.yaml, where it is read in review.
	if prep.plan.Blocked {
		return fmt.Errorf("plan blocked, nothing was applied\n" +
			"  → each block is detailed above, with what clears it")
	}

	// --fail-on before any write, and before the confirmation: the CI that
	// writes is the one that most needs the safeguard it chose. The flag comes
	// from planOptions, so it is shown in apply's help — accepting it then
	// ignoring it would be worse than not offering it.
	if err := checkFailOn(prep.plan, opts.failOn); err != nil {
		return err
	}

	// An allowed change apply could not write is a notion-seed bug: rejected
	// before the confirmation, hence before any write.
	if err := apply.Check(prep.plan, prep.snap); err != nil {
		return err
	}

	if !todo.writes() && len(todo.withheld) == 0 {
		// Plan converged. A ceremonial prompt on an empty plan would teach people
		// to type "apply" without reading.
		return nil
	}

	if todo.writes() {
		announceWhatWillHappen(out, todo, prep.cfg.Workspace.ParentPageID)
		ok, err := confirm(cmd, autoApprove)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("confirmation declined, nothing was applied\n"+
				"  → rerun and type exactly \"%s\" to apply", confirmWord)
		}
	}

	// apply is often the FIRST command that writes the state. Without this line,
	// checkWorkspaceMatch would stay disarmed forever for a project bootstrapped
	// by apply: a state without workspace_id is treated as "cannot check", and a
	// 404 from the wrong workspace would throw away a perfectly valid identity.
	if prep.snap.WorkspaceID == "" {
		prep.snap.WorkspaceID = prep.workspaceID
	}

	// The same resource serves all three roles: create, update and move to the
	// trash go through the same transport.
	res := resources.NewDatabaseResource(prep.tr, mapper.RemoteDatabaseFromJSON)
	rep, runErr := apply.Run(cmd.Context(), prep.plan, prep.snap, apply.Options{
		Dir:          opts.dir,
		ParentPageID: prep.cfg.Workspace.ParentPageID,
		Creator:      res,
		Updater:      res,
		Trasher:      res,
	})
	// The report is printed even on failure: it is what says what is done, and
	// the user needs it all the more when things went wrong.
	renderReport(out, rep, len(todo.withheld))
	if runErr != nil {
		return runErr
	}
	if !rep.Converged() {
		return fmt.Errorf(
			"apply did not converge: %d resource(s) withheld, %d mismatch(es)\n"+
				"  → each withheld resource and each mismatch is detailed above; "+
				"run `notion-seed plan` again to see what is left",
			len(todo.withheld), len(rep.Mismatches))
	}
	return nil
}

// pending counts what apply is about to do, by kind, and what it withholds.
//
// Creations, updates, destructions and cleanups are counted SEPARATELY:
// removing a stale state entry writes nothing to Notion, moving a database to
// the trash does. Mixing them up would make the confirmation sentence lie —
// the only moment the user decides, based on what is on screen. Cleanup still
// counts as a write: it is an identity thrown away, and only an import gets it
// back.
type pending struct {
	create, update, destroy, clean int
	withheld                       []withheldChange
}

// writes says whether something will be written — to Notion or to the state.
func (n pending) writes() bool {
	return n.create+n.update+n.destroy+n.clean > 0
}

// splitByWritability separates what goes to Notion, what only touches the
// local state, and what is WITHHELD.
//
// Withheld is read FIRST, as in apply.Run: it, not Target, carries the ban on
// writing. The shape of allowed changes is checked by apply.Check, not here.
func splitByWritability(p *diff.Plan) pending {
	n := pending{clean: len(p.StaleState)}
	for _, c := range p.Changes {
		if c.Withheld != "" {
			n.withheld = append(n.withheld, withheldChange{resource: c.Resource, reason: c.Withheld})
			continue
		}
		switch c.Kind {
		case resources.KindCreate:
			n.create++
		case resources.KindUpdate:
			n.update++
		case resources.KindDestroy:
			n.destroy++
		}
	}
	return n
}

// withheldChange carries the reason with the name: a withheld resource
// without a reason would leave the user guessing what to do.
type withheldChange struct {
	resource string
	reason   string
}

// announceWhatWillHappen says what will happen, by kind. A single sentence
// speaking of "writes" for everything would be wrong for the cleanup, and the
// trash has its own line: it is the only one that destroys.
func announceWhatWillHappen(w io.Writer, n pending, parentPageID string) {
	if n.create > 0 {
		fmt.Fprintf(w, "%d database(s) will be created under page %s.\n",
			n.create, parentPageID)
	}
	if n.update > 0 {
		fmt.Fprintf(w, "%d database(s) will be updated in Notion.\n", n.update)
	}
	if n.destroy > 0 {
		fmt.Fprintf(w, "%d database(s) will be moved to the trash in Notion.\n", n.destroy)
	}
	if n.clean > 0 {
		fmt.Fprintf(w, "%d stale entry(ies) will be removed from the state. "+
			"Nothing will be written to Notion for these.\n", n.clean)
	}
}

// renderWithheld prints the resources apply refuses to write because it
// cannot express them. The cause is a limit of the API, not of this version:
// waiting for a release will not change anything.
func renderWithheld(w io.Writer, withheld []withheldChange) error {
	if len(withheld) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w, "Withheld — migration required"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	for _, x := range withheld {
		if _, err := fmt.Fprintf(w, "  ~ %s\n", x.resource); err != nil {
			return err
		}
		for _, line := range strings.Split(x.reason, "\n") {
			if _, err := fmt.Fprintf(w, "      %s\n", strings.TrimSpace(line)); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}

// renderReport says what is done. The summary counts the withheld in its own
// term: an apply that fails on a withheld resource must not print a summary
// suggesting everything went through.
func renderReport(w io.Writer, rep apply.Report, withheld int) {
	for _, l := range rep.Created {
		fmt.Fprintf(w, "+ %s\n", l)
	}
	for _, l := range rep.Updated {
		fmt.Fprintf(w, "~ %s\n", l)
	}
	for _, l := range rep.Destroyed {
		fmt.Fprintf(w, "- %s\n", l)
	}
	for _, l := range rep.Cleaned {
		fmt.Fprintf(w, "- %s\n", l)
	}
	if len(rep.Mismatches) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "The API did not write what was announced")
		fmt.Fprintln(w)
		for _, l := range rep.Mismatches {
			fmt.Fprintf(w, "  %s\n", l)
		}
	}
	lines := len(rep.Created) + len(rep.Updated) + len(rep.Destroyed) + len(rep.Cleaned)
	// Mismatches count: an apply that fails on a single unconfirmed trashing
	// must keep its summary, which says nothing was done.
	if lines+withheld+len(rep.Mismatches) == 0 {
		return
	}
	// A blank line separates the summary from the report lines, and from them
	// only: the "Withheld" section, which may precede it, already ends with one.
	if lines+len(rep.Mismatches) > 0 {
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "Applied: %d created, %d updated, %d trashed, "+
		"%d state entry(ies) cleaned. Withheld: %d resource(s).\n",
		len(rep.Created), len(rep.Updated), len(rep.Destroyed), len(rep.Cleaned), withheld)
}

// confirm reads the confirmation. Only the exact word means yes: "yes",
// "APPLY" or an empty input decline.
func confirm(cmd *cobra.Command, autoApprove bool) (bool, error) {
	if autoApprove {
		return true, nil
	}
	if !isInteractive(cmd) {
		return false, errNoOneToAsk()
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Type \"%s\" to confirm: ", confirmWord)
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && line == "" {
		// The input is a terminal (isInteractive excluded /dev/null), and it
		// closed without a single byte: it is a Ctrl-D, or a closed terminal.
		// Saying "not a terminal" here would be wrong, and would send someone
		// who simply wanted not to apply towards --auto-approve.
		//
		// The newline hands control back below the prompt: without it, the
		// error would print right after `Type "apply" to confirm: `.
		fmt.Fprintln(cmd.OutOrStdout())
		if errors.Is(err, io.EOF) {
			return false, fmt.Errorf(
				"confirmation interrupted (end of input): nothing was applied\n"+
					"  → rerun the command and type \"%s\" to apply", confirmWord)
		}
		return false, fmt.Errorf(
			"failed to read the confirmation: %w — nothing was applied\n"+
				"  → rerun the command and type \"%s\" to apply", err, confirmWord)
	}
	return strings.TrimSpace(line) == confirmWord, nil
}

// errNoOneToAsk is the refusal to write when no confirmation can be obtained.
// Never a default run: apply does not run because nobody answered.
func errNoOneToAsk() error {
	return fmt.Errorf(
		"apply needs a confirmation, but none could be obtained "+
			"(standard input is not a terminal)\n"+
			"  → pass --auto-approve to apply without confirmation (that is the "+
			"CI mode), or run the command in a terminal and type \"%s\"", confirmWord)
}
