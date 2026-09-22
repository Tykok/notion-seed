// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import "github.com/spf13/cobra"

func newDiffCmd() *cobra.Command {
	opts := &planOptions{}
	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Équivalent lecture seule de plan, sans toucher au state",
		Long: "diff est identique à plan au MVP 0, puisque plan n'écrit pas encore\n" +
			"de state. Les deux commandes restent distinctes pour que l'usage en CI\n" +
			"soit stable quand plan touchera le state.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPlan(cmd, opts)
		},
	}
	opts.bind(cmd)
	return cmd
}
