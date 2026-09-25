// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tykok/notion-seed/core/config"
	"github.com/tykok/notion-seed/core/preflight"
	"github.com/tykok/notion-seed/core/providers/notion/mapper"
	"github.com/tykok/notion-seed/core/providers/notion/resources"
	"github.com/tykok/notion-seed/core/state"
)

// hex32 matches a Notion id without dashes, as it appears in URLs.
var hex32 = regexp.MustCompile(`[0-9a-fA-F]{32}`)

func newImportCmd() *cobra.Command {
	opts := &planOptions{}
	cmd := &cobra.Command{
		Use:   "import database.<key> <id-or-url>",
		Short: "Adopt an existing Notion database under a declared key",
		Long: "import writes the identity and current state of an existing database\n" +
			"to the state file, so that plan can recognize it instead of\n" +
			"proposing to recreate it.\n\n" +
			"import adopts the actual state as it is: it does not require the database\n" +
			"to already match the YAML. The plan will reveal the mismatch afterwards.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runImport(cmd, opts, args[0], args[1])
		},
	}
	opts.bind(cmd)
	// import makes no sense offline: it reads the actual state. The flag is
	// hidden from the help AND rejected if passed — a flag accepted then ignored
	// would suggest an offline import that actually called the API.
	_ = cmd.Flags().MarkHidden("skip-preflight")
	// Same treatment for --fail-on: import computes no plan, so it has no class
	// to check against. Hidden from the help AND rejected if passed.
	_ = cmd.Flags().MarkHidden("fail-on")
	return cmd
}

func runImport(cmd *cobra.Command, opts *planOptions, addr, rawID string) error {
	ctx := cmd.Context()

	if err := opts.validate(); err != nil {
		return err
	}
	if cmd.Flags().Changed("skip-preflight") {
		return fmt.Errorf(
			"import does not accept --skip-preflight\n" +
				"  → the command reads the actual state of the database to record it in the " +
				"state: it has nothing to do offline")
	}
	if cmd.Flags().Changed("fail-on") {
		return fmt.Errorf(
			"import does not accept --fail-on\n" +
				"  → import adopts the actual state as it is, it computes no plan and " +
				"so has no class to reject. Use `notion-seed diff " +
				"--fail-on=...` to keep the CI on the mismatch that follows")
	}
	key, err := parseResourceAddress(addr)
	if err != nil {
		return err
	}
	id, err := parseNotionID(rawID)
	if err != nil {
		return err
	}

	cfg, err := config.Load(opts.dir)
	if err != nil {
		return err
	}
	desired, err := findDatabase(cfg, key)
	if err != nil {
		return err
	}

	snap, err := state.Load(opts.dir)
	if err != nil {
		return err
	}
	if _, exists := snap.Databases[key]; exists {
		return fmt.Errorf(
			"database.%s is already in %s\n"+
				"  → remove its entry from %s to re-import it; re-importing without "+
				"that would overwrite an existing identity",
			key, state.FileName, state.FileName)
	}

	info, err := preflight.Check(ctx, "ntn")
	if err != nil {
		return err
	}
	if err := checkWorkspaceMatch(snap, info.WorkspaceID); err != nil {
		return err
	}

	tr := newTransport(cmd, opts)
	remote, err := resources.NewDatabaseResource(tr, mapper.RemoteDatabaseFromJSON).Read(ctx, id)
	if err != nil {
		return fmt.Errorf(
			"failed to read database %s: %w\n"+
				"  → check that the id does point to a database (open it as a full "+
				"page in Notion: its id is in the URL)", id, err)
	}
	rd, ok := remote.(resources.RemoteDatabase)
	if !ok || !rd.Found {
		return fmt.Errorf(
			"database %s not found\n"+
				"  → check the id; ntn's token sees the whole workspace without prior "+
				"sharing, so this is not a permissions problem", id)
	}
	if rd.Archived {
		return fmt.Errorf(
			"database %s is archived or in the trash\n"+
				"  → restore it in Notion before importing it: planning against a "+
				"resource in the trash produces writes that will fail", id)
	}

	adopted, orphans := state.JoinOptionKeys(state.FromRemote(rd), state.FromConfig(*desired))
	if snap.Databases == nil {
		snap.Databases = map[string]state.Database{}
	}
	snap.Databases[key] = adopted
	if snap.WorkspaceID == "" {
		snap.WorkspaceID = info.WorkspaceID
	}
	if err := state.Save(opts.dir, snap); err != nil {
		return err
	}

	cmd.Printf("database.%s imported — id %s (%d properties, %d options without a config key)\n",
		key, rd.ID, len(adopted.Properties), orphans)
	return nil
}

// parseResourceAddress reads a `database.<key>` address. Only databases can be
// imported in MVP 0; the message says so rather than leaving the user to guess
// the expected form.
func parseResourceAddress(addr string) (string, error) {
	kind, key, found := strings.Cut(addr, ".")
	if !found || kind != "database" || key == "" {
		return "", fmt.Errorf(
			"address %q not recognized\n"+
				"  → the expected form is `database.<key>`, for example `database.tasks`; "+
				"only databases can be imported today", addr)
	}
	return key, nil
}

// parseNotionID accepts a UUID, an id without dashes, or a Notion URL — the URL
// is what the user actually has at hand.
func parseNotionID(s string) (string, error) {
	s = strings.TrimSpace(s)

	compact := strings.ReplaceAll(s, "-", "")
	if m := hex32.FindString(compact); m != "" && len(compact) == 32 {
		return dashed(m), nil
	}

	// The query string AND the fragment are stripped BEFORE searching: a
	// database URL ends with `?v=<32 hex digits>`, the id of the VIEW, and a
	// "copy link to block" link ends with `#<32 hex digits>`, the id of the
	// BLOCK. Either would adopt the wrong resource, with a perfectly well-formed
	// id — hence a baffling failure at read time, not at parse time.
	path := s
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}

	// In what remains, the id is the last group of 32 hex digits: the title
	// precedes it (`.../Mes-taches-<id>`).
	all := hex32.FindAllString(strings.ReplaceAll(path, "-", ""), -1)
	if len(all) > 0 {
		return dashed(all[len(all)-1]), nil
	}
	return "", fmt.Errorf(
		"identifier %q not recognized\n"+
			"  → expected: a UUID, or the URL of the database (open it as a full page "+
			"in Notion and copy the link)", s)
}

func dashed(h string) string {
	h = strings.ToLower(h)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// findDatabase finds a declared database, and names the existing keys when it
// does not exist: looking up the exact name in one's own files is work the
// command can do.
func findDatabase(cfg *config.Config, key string) (*config.Database, error) {
	keys := make([]string, 0, len(cfg.Databases))
	for i := range cfg.Databases {
		if cfg.Databases[i].Key == key {
			return &cfg.Databases[i], nil
		}
		keys = append(keys, cfg.Databases[i].Key)
	}
	sort.Strings(keys)
	return nil, fmt.Errorf(
		"no database with key %q in the configuration\n"+
			"  → declared keys: %s. Declare the database in databases/ before "+
			"importing it: without a declaration, there is nothing to plan against it",
		key, strings.Join(keys, ", "))
}
