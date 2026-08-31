package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Version est écrasée au build via -ldflags.
var Version = "0.0.0-dev"

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "notion-seed",
		Short:         "Déclare et planifie la structure d'un workspace Notion",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCmd())
	root.AddCommand(newInitCmd())
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
