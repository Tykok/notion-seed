// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Version is overwritten at build time via -ldflags.
var Version = "0.0.0-dev"

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "notion-seed",
		Short:         "Declare and plan the structure of a Notion workspace",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newPlanCmd())
	root.AddCommand(newDiffCmd())
	root.AddCommand(newImportCmd())
	root.AddCommand(newApplyCmd())
	return root
}

func Execute() error {
	cmd := NewRootCmd()
	cmd.SetOut(os.Stdout)
	cmd.SetErr(os.Stderr)
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return err
	}
	return nil
}
