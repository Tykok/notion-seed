// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import "github.com/spf13/cobra"

func newDiffCmd() *cobra.Command {
	opts := &planOptions{}
	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Read-only equivalent of plan, without touching the state",
		Long: "diff is identical to plan in MVP 0, since plan does not write the\n" +
			"state yet. The two commands stay separate so that CI usage stays\n" +
			"stable once plan touches the state.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPlan(cmd, opts)
		},
	}
	opts.bind(cmd)
	opts.bindOut(cmd)
	return cmd
}
