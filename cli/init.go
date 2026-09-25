// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tykok/notion-seed/core/preflight"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Check notion-seed's connection to the Notion workspace",
		Long: "init does not handle authentication itself: `ntn login` is interactive\n" +
			"and stores the token in the OS keychain. init checks that ntn is\n" +
			"present, recent enough and authenticated, and says what to do otherwise.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info, err := preflight.Check(cmd.Context(), "ntn")
			var notAuth *preflight.NotAuthenticatedError
			switch {
			case errors.As(err, &notAuth):
				// The preflight message is NOT wrapped: it advises running
				// `notion-seed init`, which the user just did. init therefore
				// emits its own advice — but appends the cause, without which a
				// network outage or an ntn crash would show as "not
				// authenticated", on the command whose only job is to diagnose
				// the environment.
				return fmt.Errorf(
					"ntn is not authenticated.\n\n  First run:\n\n    ntn login\n\n"+
						"  Then rerun `notion-seed init`.\n\n  Cause: %v\n",
					notAuth.Cause)
			case err != nil:
				return err
			}
			cmd.Printf("ntn %s\n", info.NtnVersion)
			cmd.Printf("workspace: %s (%s)\n", info.WorkspaceName, info.WorkspaceID)
			cmd.Printf("signed in as: %s\n", info.BotEmail)
			return nil
		},
	}
}
