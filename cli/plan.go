// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"context"
	"errors"
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
		"mode entièrement hors ligne : ne vérifie ni ntn (présence, version, auth) "+
			"ni l'existence de la page parente. Valide la configuration et rend le "+
			"plan sans aucun appel réseau.")
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
	// Pas de fallback sur un contexte nil : cobra le peuple toujours avant RunE
	// (vérifié dans les sources de v1.10.2), et un fallback silencieux
	// masquerait une erreur de programmation au lieu de la révéler.
	ctx := cmd.Context()

	// 0. Valider les flags AVANT tout. NewTokenBucket panique sur un rate non
	// positif, et une panic est un message inutilisable pour qui a tapé
	// `--rate 0`.
	if err := opts.validate(); err != nil {
		return err
	}

	// 1. Load — valider chaque fichier, fusionner, PUIS vérifier l'unicité
	// globale des key. config.Load garantit cet ordre.

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
		tr := newTransport(cmd, opts)
		if err := checkParentPage(ctx, tr, cfg.Workspace.ParentPageID, opts.ratePerSec); err != nil {
			return err
		}
	}

	// 4. Diff — desired contre un état réel vide : tout ressort en création.
	p, err := diff.Compute(cfg, nil, nil)
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
//
// Les attentes de retry partent sur stderr, pas sur stdout : stdout ne porte que
// le plan, qui doit rester identique entre deux runs et exploitable dans un pipe.
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

// checkParentPage vérifie que la page parente est lisible. Le token de ntn
// étant scopé utilisateur, il voit tout le workspace : un 404 ici signifie
// que la page n'existe pas, pas qu'elle n'a pas été partagée.
func checkParentPage(ctx context.Context, tr transport.Transport, pageID string, ratePerSec float64) error {
	// Le schéma exige aujourd'hui parent_page_id non vide et conforme à un UUID
	// (pattern) : cette branche est donc inatteignable en pratique tant que le
	// champ reste obligatoire. Le garde-fou reste utile pour le jour où
	// parent_page_id deviendra optionnel, quand les pages seront des ressources
	// (post-MVP).
	if pageID == "" {
		return fmt.Errorf("workspace.parent_page_id est vide dans workspace.yaml")
	}
	_, err := tr.Execute(ctx, transport.APIRequest{
		Method: "GET",
		Path:   "/v1/pages/" + pageID,
	})
	if err == nil {
		return nil
	}

	// Une issue inconnue n'est pas un échec : le type le dit lui-même. La
	// rapporter comme « page illisible » serait affirmer ce qu'on ne sait pas.
	var unknown *transport.OutcomeUnknownError
	if errors.As(err, &unknown) {
		return fmt.Errorf(
			"impossible de savoir si la page parente %s est lisible: %w\n"+
				"  → relancez la commande ; si ça persiste, augmentez le timeout",
			pageID, err)
	}

	// Le raisonnement « la page n'existe pas » ne vaut QUE pour un 404. Le jeton
	// de ntn est scopé utilisateur et voit tout le workspace sans partage
	// préalable, donc un 404 ne peut pas venir d'un défaut de partage —
	// mais attacher ce raisonnement à un 400 (le schéma valide déjà le format de
	// parent_page_id ; un 400 signifie donc autre chose) ou à un 403 enverrait
	// l'utilisateur sur une fausse piste.
	var apiErr *transport.APIError
	errors.As(err, &apiErr)
	if apiErr != nil && apiErr.Status == 404 {
		return fmt.Errorf(
			"page parente %s introuvable: %w\n"+
				"  → cette page n'existe pas. Le jeton de ntn voit tout le workspace "+
				"sans partage préalable, donc ce n'est pas un problème de permissions — "+
				"vérifiez workspace.parent_page_id",
			pageID, err)
	}

	// Un 429 épuisé est l'échec non-404 le plus probable, et c'est le seul dont
	// l'action corrective est un réglage de notion-seed lui-même.
	if apiErr != nil && apiErr.Status == 429 {
		return fmt.Errorf(
			"page parente %s illisible: %w\n"+
				"  → l'API a limité le débit et les tentatives sont épuisées : "+
				"réessayez dans une minute, ou baissez `--rate` (actuellement %v appels/s)",
			pageID, err, ratePerSec)
	}

	if apiErr == nil {
		// L'erreur ne vient pas de l'API : format de sortie de ntn non reconnu,
		// binaire introuvable, appel mal construit. Elle porte déjà sa propre
		// action corrective — empiler un second conseil générique enverrait
		// chercher au mauvais endroit.
		return fmt.Errorf("page parente %s illisible: %w", pageID, err)
	}

	return fmt.Errorf(
		"page parente %s illisible: %w\n"+
			"  → vérifiez que `ntn` est authentifié sur le bon workspace "+
			"(`notion-seed init`) et que workspace.parent_page_id désigne une page "+
			"de ce workspace ; réessayez si l'API est en incident",
		pageID, err)
}
