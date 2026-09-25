// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/spf13/cobra"
	"github.com/tykok/notion-seed/core/change"
	"github.com/tykok/notion-seed/core/config"
	"github.com/tykok/notion-seed/core/diff"
	"github.com/tykok/notion-seed/core/measure"
	"github.com/tykok/notion-seed/core/preflight"
	"github.com/tykok/notion-seed/core/providers/notion/mapper"
	"github.com/tykok/notion-seed/core/providers/notion/resources"
	"github.com/tykok/notion-seed/core/providers/notion/transport"
	"github.com/tykok/notion-seed/core/state"
)

// planOptions porte les flags partagés par plan et diff.
type planOptions struct {
	dir           string
	skipPreflight bool
	ratePerSec    float64
	burst         int
	failOn        []string
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
	cmd.Flags().StringSliceVar(&o.failOn, "fail-on", nil,
		"classes de changement qui font sortir en code non nul : destructive, "+
			"silent-rewrite, unknown, migration. Vide = rien ne fait échouer.")
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
	// La valeur de --fail-on est résolue ICI, donc avant config.Load et avant le
	// moindre appel réseau. Un nom mal tapé qui ne serait refusé qu'après le
	// plan laisserait une CI passer au vert en croyant se protéger : le flag ne
	// protégerait pas, et personne ne le saurait.
	if _, err := failOnClasses(o.failOn); err != nil {
		return err
	}
	return nil
}

// failOnClasses traduit les valeurs de --fail-on en classes.
//
// La liste est ÉNUMÉRÉE, pas un seuil : `sûr`, `destructif` et `réécriture
// silencieuse` forment bien une échelle, mais un impact inconnu n'y a pas de
// place — un couple de types non mesuré peut se révéler anodin comme
// catastrophique. Le traiter comme « pire que destructif » serait aussi faux
// que l'inverse.
func failOnClasses(values []string) ([]change.Class, error) {
	byName := map[string]change.Class{
		"destructive":    change.ClassDestructive,
		"silent-rewrite": change.ClassSilentRewrite,
		"unknown":        change.ClassUnknownImpact,
		"migration":      change.ClassMigration,
	}
	out := make([]change.Class, 0, len(values))
	for _, v := range values {
		c, ok := byName[v]
		if !ok {
			return nil, fmt.Errorf(
				"--fail-on ne connaît pas %q\n"+
					"  → valeurs acceptées : destructive, silent-rewrite, unknown, "+
					"migration ; séparez-les par des virgules", v)
		}
		out = append(out, c)
	}
	return out, nil
}

// firstMatchingClass rend la première classe du plan qui figure dans la liste,
// ou ClassSafe si aucune ne correspond.
//
// La recherche porte sur les DÉTAILS, pas sur la classe agrégée de la
// ressource : c'est le détail qui porte le coût mesuré, et l'agrégat d'une
// ressource par ailleurs dangereuse ferait échouer sur une ligne qui ne coûte
// rien.
func firstMatchingClass(p *diff.Plan, classes []change.Class) change.Class {
	for _, c := range p.Changes {
		for _, d := range c.Details {
			for _, want := range classes {
				if d.Class == want {
					return want
				}
			}
		}
	}
	return change.ClassSafe
}

// checkFailOn fait sortir en code non nul si le plan porte une des classes
// énumérées par --fail-on.
//
// plan, diff ET apply passent par ici : les trois commandes partagent
// planOptions, donc les trois exposent le flag. Un flag affiché dans l'aide
// d'apply mais ignoré par apply serait pire que pas de flag du tout — la CI
// qui écrit est justement celle qui croit se protéger.
func checkFailOn(p *diff.Plan, failOn []string) error {
	classes, err := failOnClasses(failOn)
	if err != nil {
		return err
	}
	if hit := firstMatchingClass(p, classes); hit != change.ClassSafe {
		return fmt.Errorf(
			"le plan porte un changement de classe %q, refusé par --fail-on\n"+
				"  → relisez le plan ci-dessus ; retirez cette classe de --fail-on "+
				"si le changement est voulu", hit)
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

// prepared porte tout ce que plan et apply calculent de la même façon.
//
// Les deux commandes DOIVENT passer par ici : si apply recalculait autrement,
// le plan affiché et ce qui est écrit pourraient diverger — le défaut même que
// la cible résolue existe pour rendre impossible.
type prepared struct {
	cfg  *config.Config
	snap *state.Snapshot
	plan *diff.Plan
	// tr est nil sous --skip-preflight : aucun appel n'a été émis.
	tr transport.Transport
	// workspaceID est celui sur lequel ntn est authentifié. Vide sous
	// --skip-preflight, où aucun whoami n'a été fait. apply l'inscrit dans le
	// state qu'il crée : sans lui, checkWorkspaceMatch reste désarmé à vie pour
	// un projet amorcé par apply plutôt que par import.
	workspaceID string
	// measureFailures porte les comptages qui n'ont pas abouti. Ce sont des
	// incidents, pas du plan : ils s'affichent sur stderr, et ne font échouer
	// ni plan ni apply — la ligne concernée reste simplement en « impact
	// inconnu », ce qui est la vérité.
	measureFailures []string
}

func preparePlan(cmd *cobra.Command, opts *planOptions) (*prepared, error) {
	// Pas de fallback sur un contexte nil : cobra le peuple toujours avant RunE
	// (vérifié dans les sources de v1.10.2), et un fallback silencieux
	// masquerait une erreur de programmation au lieu de la révéler.
	ctx := cmd.Context()

	// 0. Valider les flags AVANT tout. NewTokenBucket panique sur un rate non
	// positif, et une panic est un message inutilisable pour qui a tapé
	// `--rate 0`.
	if err := opts.validate(); err != nil {
		return nil, err
	}

	// 1. Load — valider chaque fichier, fusionner, PUIS vérifier l'unicité
	// globale des key. config.Load garantit cet ordre.
	cfg, err := config.Load(opts.dir)
	if err != nil {
		return nil, err
	}

	// 2. State — le dernier état appliqué. Absent = premier run, tout ressort
	// en création, exactement comme avant l'existence du state.
	snap, err := state.Load(opts.dir)
	if err != nil {
		return nil, err
	}

	out := &prepared{cfg: cfg, snap: snap}
	var refreshed map[string]diff.Refreshed
	if !opts.skipPreflight {
		info, err := preflight.Check(ctx, "ntn")
		if err != nil {
			return nil, err
		}
		cmd.Printf("ntn %s — workspace %s (%s)\n\n",
			info.NtnVersion, info.WorkspaceName, info.WorkspaceID)

		if err := checkWorkspaceMatch(snap, info.WorkspaceID); err != nil {
			return nil, err
		}
		out.workspaceID = info.WorkspaceID

		out.tr = newTransport(cmd, opts)
		if err := checkParentPage(ctx, out.tr, cfg.Workspace.ParentPageID, opts.ratePerSec); err != nil {
			return nil, err
		}

		// 3. Refresh — lire l'état réel des seules ressources que le state ancre.
		// Sans id, aucune mise en correspondance n'est possible : une database
		// non importée ressort en création, ce qui est exact.
		refreshed, err = refreshManaged(ctx, out.tr, snap)
		if err != nil {
			return nil, err
		}
	}

	// 4. Diff — config, state et réel.
	out.plan, err = diff.Compute(cfg, snap, refreshed)
	if err != nil {
		return nil, err
	}

	// 5. Mesure — remplir les comptes et reclasser. Hors ligne, rien n'est
	// mesuré et chaque ligne concernée reste en « impact inconnu », ce qui est
	// la vérité : on n'a rien lu.
	if out.tr != nil {
		out.measureFailures = measure.Enrich(ctx, measure.NewCounter(out.tr),
			measuredDataSourceIDs(snap, refreshed), out.plan)
	}
	return out, nil
}

// measuredDataSourceIDs associe chaque key au data source à INTERROGER.
//
// L'id frais, celui que le refresh vient de lire, l'emporte sur celui du
// state : le plan est calculé contre le réel relu, et mesurer ailleurs
// compterait les lignes d'un autre objet. Un data source qui a changé d'id
// laisse derrière lui un ancien id qui peut être encore vivant — il appartient
// alors à une autre database, répond 200, et le 0 qui en sortirait ferait
// annoncer « rien à perdre » sur un retrait qui coûte.
//
// Le state reste le repli : sous --skip-preflight rien n'est relu, et une
// ressource relue sans data source id n'a rien de mieux à proposer.
func measuredDataSourceIDs(snap *state.Snapshot, refreshed map[string]diff.Refreshed) map[string]string {
	out := make(map[string]string, len(refreshed))
	if snap != nil {
		for key, db := range snap.Databases {
			out[key] = db.DataSourceID
		}
	}
	for key, r := range refreshed {
		// Une ressource introuvable ou archivée n'a pas été lue : son
		// Database est vide, et l'écraser avec du vide effacerait le repli.
		if r.Missing || r.Database.DataSourceID == "" {
			continue
		}
		out[key] = r.Database.DataSourceID
	}
	return out
}

// reportMeasureFailures dit ce qui n'a pas pu être compté, sur stderr : ce sont
// des incidents, pas du plan. stdout ne porte que le plan, qui doit rester
// exploitable dans un pipe.
func reportMeasureFailures(cmd *cobra.Command, prep *prepared) {
	for _, f := range prep.measureFailures {
		fmt.Fprintln(cmd.ErrOrStderr(), "comptage impossible — "+f)
	}
}

func runPlan(cmd *cobra.Command, opts *planOptions) error {
	prep, err := preparePlan(cmd, opts)
	if err != nil {
		return err
	}
	p := prep.plan
	reportMeasureFailures(cmd, prep)

	// 6. Render — texte brut sur stdout.
	if err := diff.Render(cmd.OutOrStdout(), p); err != nil {
		return err
	}
	if p.Blocked {
		// Le rendu ci-dessus détaille déjà chaque raison de blocage avec son
		// action corrective (section « Plan bloqué »). Cette erreur ne les
		// répète pas, mais porte quand même sa propre flèche : elle atteint
		// l'utilisateur telle quelle sur stderr (cli/root.go l'imprime), et la
		// contrainte du projet sur les messages d'erreur est inconditionnelle.
		return fmt.Errorf("plan bloqué\n" +
			"  → chaque blocage est détaillé ci-dessus, avec ce qui le lève")
	}
	// APRÈS le rendu : --fail-on fait sortir en code non nul, il ne prive pas
	// l'utilisateur du plan qui explique pourquoi. Le message d'erreur renvoie
	// à ce qui vient d'être écrit.
	return checkFailOn(p, opts.failOn)
}

// checkWorkspaceMatch refuse un state venu d'un autre workspace.
//
// Un workspace_id vide n'est PAS un désaccord : c'est un state écrit avant que
// le champ existe, ou à la main. On ne peut pas vérifier, donc on ne prétend
// pas savoir.
func checkWorkspaceMatch(snap *state.Snapshot, workspaceID string) error {
	if snap == nil || snap.WorkspaceID == "" || snap.WorkspaceID == workspaceID {
		return nil
	}
	return fmt.Errorf(
		"%s décrit le workspace %s, mais ntn est authentifié sur %s\n"+
			"  → les identités du state n'existent pas dans ce workspace. Authentifiez "+
			"ntn sur le bon workspace (`ntn login`), ou travaillez dans le dossier de "+
			"configuration correspondant",
		state.FileName, snap.WorkspaceID, workspaceID)
}

// refreshManaged lit l'état réel de chaque ressource ancrée par le state.
//
// Une ressource introuvable ou archivée n'est pas une erreur de la commande :
// c'est un fait que le plan doit rapporter et sur lequel il bloquera. La faire
// remonter en erreur priverait l'utilisateur du reste du plan.
func refreshManaged(ctx context.Context, tr transport.Transport, snap *state.Snapshot) (map[string]diff.Refreshed, error) {
	if snap == nil || len(snap.Databases) == 0 {
		return nil, nil
	}
	res := resources.NewDatabaseResource(tr, mapper.RemoteDatabaseFromJSON)
	out := make(map[string]diff.Refreshed, len(snap.Databases))

	keys := make([]string, 0, len(snap.Databases))
	for key := range snap.Databases {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		if snap.Databases[key].ID == "" {
			return nil, fmt.Errorf(
				"database.%s n'a pas d'identifiant dans %s\n"+
					"  → l'entrée est incomplète : restaurez le fichier depuis git, ou "+
					"retirez cette entrée et ré-importez la database",
				key, state.FileName)
		}
		remote, err := res.Read(ctx, snap.Databases[key].ID)
		if err != nil {
			var apiErr *transport.APIError
			if errors.As(err, &apiErr) && apiErr.Status == 404 {
				out[key] = diff.Refreshed{Missing: true, Reason: "introuvable (404)"}
				continue
			}
			return nil, fmt.Errorf(
				"lecture de database.%s (id %s) impossible: %w\n"+
					"  → réessayez ; si la database a été supprimée, retirez son entrée de %s",
				key, snap.Databases[key].ID, err, state.FileName)
		}
		rd, ok := remote.(resources.RemoteDatabase)
		if !ok || !rd.Found {
			out[key] = diff.Refreshed{Missing: true, Reason: "introuvable"}
			continue
		}
		if rd.Archived {
			out[key] = diff.Refreshed{Missing: true, Reason: "archivée ou en corbeille"}
			continue
		}
		out[key] = diff.Refreshed{Database: state.FromRemote(rd), DataSources: rd.DataSourceCount}
	}
	return out, nil
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
	resp, err := tr.Execute(ctx, transport.APIRequest{
		Method: "GET",
		Path:   "/v1/pages/" + pageID,
	})
	if err == nil {
		return checkParentPageAlive(pageID, resp.Body)
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

// checkParentPageAlive refuse une page parente à la corbeille.
//
// Une page à la corbeille se lit en 200 : le code de statut ne dit rien, seuls
// ses champs archived et in_trash le disent. Mesuré le 2026-09-25 : sans ce
// contrôle, plan annonçait « Aucun changement » alors que toute écriture sous
// la page est refusée (`400 validation_error — Can't edit page on block with
// an archived ancestor`).
//
// Seule une page parente mise à la corbeille ELLE-MÊME est détectée ici. Quand
// c'est un de ses ancêtres qui y est, rien ne dit qu'elle porte in_trash : une
// database sous un ancêtre à la corbeille se lit bien archived:false,
// in_trash:false (mesuré). Ce cas-là n'est donc pas couvert, et ce contrôle ne
// prétend pas le couvrir.
//
// Une réponse qui ne porte AUCUN des deux champs est refusée plutôt que lue
// comme « vivante » : le silence de l'API n'est pas une mesure, et c'est
// exactement l'affirmation que ce contrôle existe pour ne plus faire.
func checkParentPageAlive(pageID string, body []byte) error {
	var page struct {
		Archived *bool `json:"archived"`
		InTrash  *bool `json:"in_trash"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return fmt.Errorf(
			"impossible de savoir si la page parente %s est à la corbeille : "+
				"réponse de l'API illisible: %v\n"+
				"  → réessayez ; si ça persiste, %s",
			pageID, err, preflight.PinNtnHint())
	}
	if page.Archived == nil && page.InTrash == nil {
		return fmt.Errorf(
			"impossible de savoir si la page parente %s est à la corbeille : "+
				"la réponse de l'API ne porte ni archived ni in_trash\n"+
				"  → réessayez ; si ça persiste, %s",
			pageID, preflight.PinNtnHint())
	}
	if (page.Archived != nil && *page.Archived) || (page.InTrash != nil && *page.InTrash) {
		return fmt.Errorf(
			"la page parente %s est à la corbeille : Notion refuse d'écrire sous elle\n"+
				"  → restaurez-la depuis la corbeille de Notion, ou faites pointer "+
				"workspace.parent_page_id vers une page vivante",
			pageID)
	}
	return nil
}
