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
		Short: "Vérifie la connexion de notion-seed au workspace Notion",
		Long: "init ne gère pas l'authentification lui-même : `ntn login` est interactif\n" +
			"et stocke le jeton dans le keychain de l'OS. init vérifie que ntn est\n" +
			"présent, assez récent et authentifié, et indique quoi faire sinon.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info, err := preflight.Check(cmd.Context(), "ntn")
			switch {
			case errors.Is(err, preflight.ErrNotAuthenticated):
				// On n'enveloppe PAS le message de preflight ici : il conseille de
				// lancer `notion-seed init`, ce que l'utilisateur vient de faire.
				// init a un seul rôle, l'authentification, donc « pas authentifié »
				// n'y est pas ambigu et n'a pas besoin de la cause détaillée — que
				// le chemin `plan` affiche, lui.
				return fmt.Errorf(
					"ntn n'est pas authentifié.\n\n  Lancez d'abord :\n\n    ntn login\n\n  Puis relancez `notion-seed init`.\n")
			case err != nil:
				return err
			}
			cmd.Printf("ntn %s\n", info.NtnVersion)
			cmd.Printf("workspace : %s (%s)\n", info.WorkspaceName, info.WorkspaceID)
			cmd.Printf("connecté en tant que : %s\n", info.BotEmail)
			return nil
		},
	}
}
