package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tykok/notion-seed/core/config"
	"github.com/tykok/notion-seed/core/diff"
	"github.com/tykok/notion-seed/core/preflight"
	"github.com/tykok/notion-seed/core/providers/notion/transport"
)

// planOptions porte les flags partagés par plan et diff.
type planOptions struct {
	dir           string
	skipPreflight bool
	ratePerSec    float64
	burst         int
}

func (o *planOptions) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&o.dir, "dir", ".",
		"dossier de configuration (contient workspace.yaml et databases/)")
	cmd.Flags().BoolVar(&o.skipPreflight, "skip-preflight", false,
		"ne pas vérifier ntn (tests et CI hors ligne)")
	cmd.Flags().Float64Var(&o.ratePerSec, "rate", transport.DefaultRatePerSec,
		"plafond d'appels API par seconde")
	cmd.Flags().IntVar(&o.burst, "burst", transport.DefaultBurst,
		"nombre d'appels tolérés en rafale")
}

// validate rejette les valeurs de flags que le rate limiter refuse. Sans ça,
// `--rate 0` remonterait en panic depuis NewTokenBucket, ce qui est un message
// inutilisable pour l'utilisateur.
func (o *planOptions) validate() error {
	if o.ratePerSec <= 0 {
		return fmt.Errorf("--rate doit être strictement positif, reçu %v", o.ratePerSec)
	}
	if o.burst <= 0 {
		return fmt.Errorf("--burst doit être strictement positif, reçu %d", o.burst)
	}
	return nil
}

func newPlanCmd() *cobra.Command {
	opts := &planOptions{}
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Calcule et affiche les changements, sans rien appliquer",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPlan(cmd, opts)
		},
	}
	opts.bind(cmd)
	return cmd
}

func runPlan(cmd *cobra.Command, opts *planOptions) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	// 1. Load — valider chaque fichier, fusionner, PUIS vérifier l'unicité
	// globale des key. config.Load garantit cet ordre.
	if err := opts.validate(); err != nil {
		return err
	}

	cfg, err := config.Load(opts.dir)
	if err != nil {
		return err
	}

	// 2. Preflight — ntn présent, assez récent, authentifié. Le workspace est
	// affiché : l'utilisateur doit voir sur lequel il opère avant tout apply.
	if !opts.skipPreflight {
		info, err := preflight.Check(ctx, "ntn")
		if err != nil {
			return err
		}
		cmd.Printf("ntn %s — workspace %s (%s)\n\n",
			info.NtnVersion, info.WorkspaceName, info.WorkspaceID)

		// 3. Refresh — vérifier que la page parente existe et est accessible.
		// Aucune ressource n'est mise en correspondance au MVP 0 : sans state,
		// il n'y a pas d'ancre d'identité fiable.
		tr := newTransport(opts)
		if err := checkParentPage(ctx, tr, cfg.Workspace.ParentPageID); err != nil {
			return err
		}
	}

	// 4. Diff — desired contre un état réel vide : tout ressort en création.
	p, err := diff.Compute(cfg, nil)
	if err != nil {
		return err
	}

	// 5. Render — texte brut sur stdout.
	if err := diff.Render(cmd.OutOrStdout(), p); err != nil {
		return err
	}
	if p.Blocked {
		return fmt.Errorf("plan bloqué par au moins un changement refusé par défaut")
	}
	return nil
}

// newTransport assemble la pile : shell-out vers ntn, entouré du rate limiter
// partagé et de la politique de retry.
func newTransport(opts *planOptions) transport.Transport {
	clock := transport.RealClock{}
	return transport.NewRetrying(
		transport.NewNtnShell(),
		transport.NewTokenBucket(opts.ratePerSec, opts.burst, clock),
		transport.DefaultRetryPolicy(),
		clock,
	)
}

// checkParentPage vérifie que la page parente est lisible. Le token de ntn
// étant scopé utilisateur, il voit tout le workspace : un 404 ici signifie
// que la page n'existe pas, pas qu'elle n'a pas été partagée.
func checkParentPage(ctx context.Context, tr transport.Transport, pageID string) error {
	if pageID == "" {
		return fmt.Errorf("workspace.parent_page_id est vide dans workspace.yaml")
	}
	_, err := tr.Execute(ctx, transport.APIRequest{
		Method: "GET",
		Path:   "/v1/pages/" + pageID,
	})
	if err != nil {
		return fmt.Errorf(
			"page parente %s illisible: %w\n"+
				"  → vérifiez workspace.parent_page_id ; le jeton de ntn voit tout le "+
				"workspace, donc un 404 signifie que la page n'existe pas",
			pageID, err)
	}
	return nil
}
