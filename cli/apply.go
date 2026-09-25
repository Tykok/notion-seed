// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tykok/notion-seed/core/apply"
	"github.com/tykok/notion-seed/core/diff"
	"github.com/tykok/notion-seed/core/providers/notion/mapper"
	"github.com/tykok/notion-seed/core/providers/notion/resources"
)

// confirmWord est le mot à taper. En entier, et pas une simple entrée : une
// confirmation qu'on donne sans lire ne protège de rien.
const confirmWord = "apply"

// isInteractive dit si l'entrée standard est un terminal.
//
// C'est une variable pour que les tests puissent l'imposer : un périphérique
// caractère n'est pas simulable dans `go test`, et ajouter un flag de
// production pour ça exposerait un contournement dans le binaire livré.
var isInteractive = func(cmd *cobra.Command) bool {
	f, ok := cmd.InOrStdin().(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func newApplyCmd() *cobra.Command {
	opts := &planOptions{}
	autoApprove := false

	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Applique le plan : crée et modifie les databases déclarées, et met le state à jour",
		Long: "apply recalcule le plan, l'affiche, demande confirmation, puis écrit.\n" +
			"Il ne prend aucun argument : il n'y a pas de fichier de plan à rejouer,\n" +
			"donc pas de plan périmé à appliquer par mégarde.\n\n" +
			"Cette version écrit les créations, les modifications, et le nettoyage des\n" +
			"entrées de state dont la ressource a déjà disparu. Une modification que\n" +
			"l'API ne sait pas exprimer — renommer une option, changer sa couleur — est\n" +
			"nommée sous « Retenu », avec la migration à faire à la main. Les\n" +
			"destructions sont nommées sous « Non appliqué » plutôt que tentées à\n" +
			"moitié. apply sort en code non nul tant qu'il reste du travail.\n\n" +
			"La confirmation ne lève aucun blocage : un plan bloqué par une ressource\n" +
			"gérée que Notion ne connaît plus n'atteint jamais le prompt.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runApply(cmd, opts, autoApprove)
		},
	}
	opts.bind(cmd)
	cmd.Flags().BoolVar(&autoApprove, "auto-approve", false,
		"applique sans demander confirmation. Indispensable hors terminal (CI).")
	// Écrire hors ligne n'a pas de sens : le flag est masqué de l'aide ET refusé
	// s'il est passé — un flag accepté puis ignoré ferait croire à un apply hors
	// ligne qui a en fait appelé l'API.
	_ = cmd.Flags().MarkHidden("skip-preflight")
	return cmd
}

func runApply(cmd *cobra.Command, opts *planOptions, autoApprove bool) error {
	if cmd.Flags().Changed("skip-preflight") {
		return fmt.Errorf(
			"apply n'accepte pas --skip-preflight\n" +
				"  → la commande écrit dans Notion après avoir lu l'état réel : elle n'a " +
				"rien à faire hors ligne. Utilisez `notion-seed plan --skip-preflight` " +
				"pour valider la configuration sans réseau")
	}

	prep, err := preparePlan(cmd, opts)
	if err != nil {
		return err
	}
	reportMeasureFailures(cmd, prep)

	out := cmd.OutOrStdout()
	// Sans la ligne d'agrégat : elle couvre tout le plan, alors qu'apply n'en
	// écrit qu'une partie. apply annonce lui-même ce qu'il va écrire, juste
	// avant la confirmation.
	if err := diff.RenderWithoutImpact(out, prep.plan); err != nil {
		return err
	}

	toCreate, toUpdate, toClean, withheld, skipped := splitByWritability(prep.plan)
	if err := renderWithheld(out, withheld); err != nil {
		return err
	}
	if err := renderSkipped(out, skipped); err != nil {
		return err
	}

	// Le blocage gagne sur tout, --auto-approve compris. Le consentement est
	// déclaratif : il se donne dans workspace.yaml, où il se relit en revue.
	if prep.plan.Blocked {
		return fmt.Errorf("plan bloqué, rien n'a été appliqué\n" +
			"  → chaque blocage est détaillé ci-dessus, avec ce qui le lève")
	}

	// --fail-on avant toute écriture, et avant la confirmation : la CI qui écrit
	// est celle qui a le plus besoin du garde-fou qu'elle a choisi. Le flag vient
	// de planOptions, donc il est affiché dans l'aide d'apply — l'accepter puis
	// l'ignorer serait pire que ne pas l'offrir.
	if err := checkFailOn(prep.plan, opts.failOn); err != nil {
		return err
	}

	if toCreate == 0 && toUpdate == 0 && toClean == 0 && len(withheld) == 0 && len(skipped) == 0 {
		// Plan convergé. Un prompt de cérémonie sur un plan vide apprendrait à
		// taper « apply » sans lire.
		return nil
	}

	if toCreate > 0 || toUpdate > 0 || toClean > 0 {
		announceWhatWillHappen(out, toCreate, toUpdate, toClean, prep.cfg.Workspace.ParentPageID)
		ok, err := confirm(cmd, autoApprove)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("confirmation refusée, rien n'a été appliqué\n"+
				"  → relancez et tapez exactement %q pour appliquer", confirmWord)
		}
	}

	// apply est souvent la PREMIÈRE commande qui écrit le state. Sans cette
	// ligne, checkWorkspaceMatch resterait désarmé à vie pour un projet amorcé
	// par apply : un state sans workspace_id est traité comme « on ne peut pas
	// vérifier », et un 404 venu du mauvais workspace ferait jeter une identité
	// parfaitement valide.
	if prep.snap.WorkspaceID == "" {
		prep.snap.WorkspaceID = prep.workspaceID
	}

	// La même ressource sert aux deux rôles : créer et modifier passent par le
	// même transport et le même décodeur de relecture.
	res := resources.NewDatabaseResource(prep.tr, mapper.RemoteDatabaseFromJSON)
	rep, runErr := apply.Run(cmd.Context(), prep.plan, prep.snap, apply.Options{
		Dir:          opts.dir,
		ParentPageID: prep.cfg.Workspace.ParentPageID,
		Creator:      res,
		Updater:      res,
	})
	// Le compte rendu est affiché même en échec : c'est lui qui dit ce qui est
	// acquis, et l'utilisateur en a d'autant plus besoin quand ça s'est mal
	// passé.
	renderReport(out, rep, len(withheld), len(skipped))
	if runErr != nil {
		return runErr
	}
	if !rep.Converged() {
		return fmt.Errorf(
			"apply n'a pas convergé : %d ressource(s) retenue(s), %d changement(s) "+
				"non appliqué(s), %d écart(s)\n"+
				"  → chaque ressource retenue ou non appliquée est détaillée ci-dessus, "+
				"avec ce qui la débloque ; relancez `notion-seed plan` pour voir ce qui reste",
			len(withheld), len(skipped), len(rep.Mismatches))
	}
	return nil
}

// splitByWritability sépare ce qui part vers Notion, ce qui ne touche que le
// state local, ce qui est RETENU, et ce qu'apply ne sait pas encore écrire.
//
// Créations, modifications et nettoyages sont comptés SÉPARÉMENT : retirer une
// entrée de state obsolète n'écrit rien dans Notion, et les confondre ferait
// mentir la phrase de confirmation — le seul moment où l'utilisateur décide, sur
// la foi de ce qui est à l'écran. Le nettoyage compte pourtant comme une
// écriture : c'est une identité qu'on jette, et elle ne se retrouve que par un
// import.
//
// Retenu et non appliqué ne se confondent pas : le premier demande une action de
// l'utilisateur — migrer des lignes à la main — le second demande d'attendre une
// version. Les mélanger enverrait l'utilisateur migrer des lignes pour une
// database qu'il suffit d'attendre.
//
// Withheld est lu EN PREMIER, comme dans apply.Run : c'est lui, et non Target,
// qui porte l'interdiction d'écrire.
func splitByWritability(p *diff.Plan) (toCreate, toUpdate, toClean int, withheld []withheldChange, skipped []skippedChange) {
	toClean = len(p.StaleState)
	for _, c := range p.Changes {
		if c.Withheld != "" {
			withheld = append(withheld, withheldChange{resource: c.Resource, reason: c.Withheld})
			continue
		}
		switch {
		case c.Kind == resources.KindCreate && c.Target != nil:
			toCreate++
		case c.Kind == resources.KindUpdate && c.Target != nil:
			toUpdate++
		default:
			skipped = append(skipped, skippedChange{resource: c.Resource, kind: c.Kind})
		}
	}
	return toCreate, toUpdate, toClean, withheld, skipped
}

// withheldChange porte la raison avec le nom : une ressource retenue sans
// motif renverrait l'utilisateur deviner quoi faire.
type withheldChange struct {
	resource string
	reason   string
}

// skippedChange porte le Kind en plus du nom : sans lui, une destruction en
// attente serait présentée comme une modification de propriétés, et le message
// enverrait l'utilisateur faire la mauvaise chose sur l'opération la plus
// destructrice du produit.
type skippedChange struct {
	resource string
	kind     resources.ChangeKind
}

// announceWhatWillHappen dit ce qui va se passer, par nature. Une seule phrase
// qui parlerait d'« écritures » pour les deux serait fausse pour le nettoyage.
func announceWhatWillHappen(w io.Writer, toCreate, toUpdate, toClean int, parentPageID string) {
	if toCreate > 0 {
		fmt.Fprintf(w, "%d database(s) vont être créées dans la page %s.\n",
			toCreate, parentPageID)
	}
	if toUpdate > 0 {
		fmt.Fprintf(w, "%d database(s) vont être modifiées dans Notion.\n", toUpdate)
	}
	if toClean > 0 {
		fmt.Fprintf(w, "%d entrée(s) obsolètes vont être retirées du state. "+
			"Rien ne sera écrit dans Notion pour celles-là.\n", toClean)
	}
}

// renderWithheld affiche les ressources qu'apply refuse d'écrire faute de
// pouvoir l'exprimer. Cette section, contrairement à la suivante, ne disparaîtra
// pas : sa cause est une limite de l'API, pas une limite de cette version.
func renderWithheld(w io.Writer, withheld []withheldChange) error {
	if len(withheld) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w, "Retenu — migration requise"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	for _, x := range withheld {
		if _, err := fmt.Fprintf(w, "  ~ %s\n", x.resource); err != nil {
			return err
		}
		for _, line := range strings.Split(x.reason, "\n") {
			if _, err := fmt.Fprintf(w, "      %s\n", strings.TrimSpace(line)); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}

// renderSkipped affiche ce qu'apply ne sait pas encore écrire : depuis que les
// modifications s'écrivent, les seules destructions. La section disparaîtra
// d'elle-même quand destroy arrivera : le contrat de sortie, lui, ne bougera
// pas.
func renderSkipped(w io.Writer, skipped []skippedChange) error {
	if len(skipped) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w,
		"Non appliqué par cette version — apply n'écrit pas encore les destructions"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	for _, s := range skipped {
		// Le repli ne devrait plus servir : une création ou une modification
		// sans cible ne sort pas du plan. S'il sert quand même, il ne prétend
		// rien sur ce qui est en attente.
		marker, hint := "~", "notion-seed ne sait pas encore écrire ce changement. "+
			"Appliquez-le dans Notion, ou attendez."
		if s.kind == resources.KindDestroy {
			// Le marqueur DOIT correspondre à celui du plan affiché plus haut, et
			// l'action corrective à l'opération réellement en attente.
			marker, hint = "-", "la destruction des databases arrive dans une version "+
				"suivante. Archivez-la dans Notion, ou attendez."
		}
		if _, err := fmt.Fprintf(w, "  %s %s\n", marker, s.resource); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "      → %s\n", hint); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}

// renderReport dit ce qui est acquis. Le bilan compte le retenu et le non
// appliqué chacun dans son terme : un « Non appliqué : 0 » affiché pendant
// qu'apply échoue sur une ressource retenue contredirait le code de sortie.
func renderReport(w io.Writer, rep apply.Report, withheld, skipped int) {
	for _, l := range rep.Created {
		fmt.Fprintf(w, "+ %s\n", l)
	}
	for _, l := range rep.Updated {
		fmt.Fprintf(w, "~ %s\n", l)
	}
	for _, l := range rep.Cleaned {
		fmt.Fprintf(w, "- %s\n", l)
	}
	if len(rep.Mismatches) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "L'API n'a pas écrit ce qui était annoncé")
		fmt.Fprintln(w)
		for _, l := range rep.Mismatches {
			fmt.Fprintf(w, "  %s\n", l)
		}
	}
	if len(rep.Created)+len(rep.Updated)+len(rep.Cleaned)+withheld+skipped > 0 {
		fmt.Fprintf(w, "\nAppliqué : %d création(s), %d modification(s), "+
			"%d entrée(s) de state nettoyée(s). Retenu : %d. Non appliqué : %d changement(s).\n",
			len(rep.Created), len(rep.Updated), len(rep.Cleaned), withheld, skipped)
	}
}

// confirm lit la confirmation. Seul le mot exact vaut oui : « oui », « APPLY »
// ou une entrée vide refusent.
func confirm(cmd *cobra.Command, autoApprove bool) (bool, error) {
	if autoApprove {
		return true, nil
	}
	if !isInteractive(cmd) {
		return false, errNoOneToAsk()
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Confirmez en tapant « %s » : ", confirmWord)
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && line == "" {
		// Fin de fichier sans un seul octet : personne n'a répondu. C'est le cas
		// d'une entrée standard branchée sur /dev/null, que le test de
		// périphérique caractère ci-dessus ne SAIT PAS distinguer d'un terminal —
		// /dev/null en est un. Même situation qu'un pipe fermé, donc même
		// message : il faut --auto-approve.
		return false, errNoOneToAsk()
	}
	return strings.TrimSpace(line) == confirmWord, nil
}

// errNoOneToAsk est le refus d'écrire quand aucune confirmation ne peut être
// obtenue. Jamais une exécution par défaut : apply ne s'exécute pas parce que
// personne ne répondait.
func errNoOneToAsk() error {
	return fmt.Errorf(
		"apply a besoin d'une confirmation, mais aucune n'a pu être obtenue "+
			"(l'entrée standard n'est pas un terminal)\n"+
			"  → passez --auto-approve pour appliquer sans confirmation (c'est le mode "+
			"CI), ou lancez la commande dans un terminal et tapez %q", confirmWord)
}
