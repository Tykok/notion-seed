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
		Short: "Applique le plan : crée les databases déclarées et met le state à jour",
		Long: "apply recalcule le plan, l'affiche, demande confirmation, puis écrit.\n" +
			"Il ne prend aucun argument : il n'y a pas de fichier de plan à rejouer,\n" +
			"donc pas de plan périmé à appliquer par mégarde.\n\n" +
			"Cette version n'écrit QUE les créations, et le nettoyage des entrées de\n" +
			"state dont la ressource a déjà disparu. Tout le reste est nommé sous\n" +
			"« Non appliqué » plutôt que tenté à moitié, et apply sort en code non nul\n" +
			"tant qu'il reste du travail.\n\n" +
			"La confirmation ne lève aucun garde-fou : un plan bloqué par lifecycle\n" +
			"n'atteint jamais le prompt.",
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
	if err := diff.Render(out, prep.plan); err != nil {
		return err
	}

	toCreate, toClean, skipped := splitByWritability(prep.plan)
	if err := renderSkipped(out, skipped); err != nil {
		return err
	}

	// Le blocage gagne sur tout, --auto-approve compris. Le consentement est
	// déclaratif : il se donne dans workspace.yaml, où il se relit en revue.
	if prep.plan.Blocked {
		return fmt.Errorf("plan bloqué, rien n'a été appliqué\n" +
			"  → chaque blocage est détaillé ci-dessus, avec ce qui le lève")
	}

	if toCreate == 0 && toClean == 0 && len(skipped) == 0 {
		// Plan convergé. Un prompt de cérémonie sur un plan vide apprendrait à
		// taper « apply » sans lire.
		return nil
	}

	if toCreate > 0 || toClean > 0 {
		announceWhatWillHappen(out, toCreate, toClean, prep.cfg.Workspace.ParentPageID)
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

	res := resources.NewDatabaseResource(prep.tr, mapper.RemoteDatabaseFromJSON)
	rep, runErr := apply.Run(cmd.Context(), prep.plan, prep.snap, apply.Options{
		Dir:          opts.dir,
		ParentPageID: prep.cfg.Workspace.ParentPageID,
		Creator:      res,
	})
	// Le compte rendu est affiché même en échec : c'est lui qui dit ce qui est
	// acquis, et l'utilisateur en a d'autant plus besoin quand ça s'est mal
	// passé.
	renderReport(out, rep, skipped)
	if runErr != nil {
		return runErr
	}
	if !rep.Converged() {
		return fmt.Errorf(
			"apply n'a pas convergé : %d changement(s) non appliqué(s), %d écart(s)\n"+
				"  → relancez `notion-seed plan` pour voir ce qui reste",
			len(rep.Skipped), len(rep.Mismatches))
	}
	return nil
}

// splitByWritability compte ce qu'apply sait écrire et nomme ce qu'il ne sait
// pas écrire.
//
// Le nettoyage d'une entrée de state obsolète compte comme une écriture : c'est
// une identité qu'on jette, et elle ne se retrouve que par un import.
// splitByWritability sépare ce qui part vers Notion, ce qui ne touche que le
// state local, et ce qu'apply ne sait pas écrire.
//
// Les deux premiers sont comptés SÉPARÉMENT : retirer une entrée de state
// obsolète n'écrit rien dans Notion, et les confondre ferait mentir la phrase
// de confirmation — le seul moment où l'utilisateur décide, sur la foi de ce
// qui est à l'écran.
func splitByWritability(p *diff.Plan) (toCreate, toClean int, skipped []skippedChange) {
	toClean = len(p.StaleState)
	for _, c := range p.Changes {
		// Une cible nulle EST l'interdiction d'écrire : voir diff.Result.Target.
		if c.Kind == resources.KindCreate && c.Target != nil {
			toCreate++
			continue
		}
		skipped = append(skipped, skippedChange{resource: c.Resource, kind: c.Kind})
	}
	return toCreate, toClean, skipped
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
func announceWhatWillHappen(w io.Writer, toCreate, toClean int, parentPageID string) {
	if toCreate > 0 {
		fmt.Fprintf(w, "%d database(s) vont être créées dans la page %s.\n",
			toCreate, parentPageID)
	}
	if toClean > 0 {
		fmt.Fprintf(w, "%d entrée(s) obsolètes vont être retirées du state. "+
			"Rien ne sera écrit dans Notion pour celles-là.\n", toClean)
	}
}

// renderSkipped affiche ce qu'apply ne sait pas encore écrire. La section
// disparaîtra d'elle-même quand update puis destroy arriveront : le contrat de
// sortie, lui, ne bougera pas.
func renderSkipped(w io.Writer, skipped []skippedChange) error {
	if len(skipped) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w,
		"Non appliqué par cette version — apply n'écrit que les créations"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	for _, s := range skipped {
		marker, hint := "~", "les modifications de propriétés arrivent dans une "+
			"version suivante. Appliquez-les dans Notion, ou attendez."
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

func renderReport(w io.Writer, rep apply.Report, skipped []skippedChange) {
	for _, l := range rep.Created {
		fmt.Fprintf(w, "+ %s\n", l)
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
	if len(rep.Created)+len(rep.Cleaned)+len(skipped) > 0 {
		fmt.Fprintf(w, "\nAppliqué : %d création(s), %d entrée(s) de state nettoyée(s). "+
			"Non appliqué : %d changement(s).\n",
			len(rep.Created), len(rep.Cleaned), len(skipped))
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
