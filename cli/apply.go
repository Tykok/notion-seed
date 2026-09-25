// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bufio"
	"errors"
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
//
// /dev/null est écarté nommément : c'est un périphérique caractère, comme un
// terminal, et le test de mode ne sait pas les distinguer. Sans cette
// exclusion, une entrée branchée sur /dev/null passerait pour un terminal, et
// sa fin d'entrée immédiate pour un Ctrl-D — confirm dirait alors « confirmation
// interrompue » à qui n'a jamais eu de terminal, au lieu de réclamer
// --auto-approve.
var isInteractive = func(cmd *cobra.Command) bool {
	f, ok := cmd.InOrStdin().(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(info, null) {
		return false
	}
	return true
}

func newApplyCmd() *cobra.Command {
	opts := &planOptions{}
	autoApprove := false

	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Applique le plan : crée, modifie et met à la corbeille les databases, et met le state à jour",
		Long: "apply recalcule le plan, l'affiche, demande confirmation, puis écrit.\n" +
			"Il ne prend aucun argument : il n'y a pas de fichier de plan à rejouer,\n" +
			"donc pas de plan périmé à appliquer par mégarde.\n\n" +
			"apply écrit les créations, les modifications, les destructions — une\n" +
			"database que le YAML ne déclare plus est mise à la corbeille de Notion —\n" +
			"et le nettoyage des entrées de state dont la ressource a déjà disparu.\n" +
			"Une modification que l'API ne sait pas exprimer — renommer une option,\n" +
			"changer sa couleur — est nommée sous « Retenu », avec la migration à faire\n" +
			"à la main, et apply sort en code non nul tant qu'elle reste.\n\n" +
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
	// La ligne d'agrégat ne compte que ce qu'apply va écrire : une ressource
	// retenue garde un impact qu'apply ne causera pas, et l'annoncer ici le
	// ferait mentir juste avant la confirmation.
	if err := diff.RenderForApply(out, prep.plan); err != nil {
		return err
	}

	todo := splitByWritability(prep.plan)
	if err := renderWithheld(out, todo.withheld); err != nil {
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

	// Un changement autorisé qu'apply ne saurait pas écrire est un défaut
	// interne : refusé avant la confirmation, donc avant la moindre écriture.
	if err := apply.Check(prep.plan, prep.snap); err != nil {
		return err
	}

	if !todo.writes() && len(todo.withheld) == 0 {
		// Plan convergé. Un prompt de cérémonie sur un plan vide apprendrait à
		// taper « apply » sans lire.
		return nil
	}

	if todo.writes() {
		announceWhatWillHappen(out, todo, prep.cfg.Workspace.ParentPageID)
		ok, err := confirm(cmd, autoApprove)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("confirmation refusée, rien n'a été appliqué\n"+
				"  → relancez et tapez exactement « %s » pour appliquer", confirmWord)
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

	// La même ressource sert aux trois rôles : créer, modifier et mettre à la
	// corbeille passent par le même transport.
	res := resources.NewDatabaseResource(prep.tr, mapper.RemoteDatabaseFromJSON)
	rep, runErr := apply.Run(cmd.Context(), prep.plan, prep.snap, apply.Options{
		Dir:          opts.dir,
		ParentPageID: prep.cfg.Workspace.ParentPageID,
		Creator:      res,
		Updater:      res,
		Trasher:      res,
	})
	// Le compte rendu est affiché même en échec : c'est lui qui dit ce qui est
	// acquis, et l'utilisateur en a d'autant plus besoin quand ça s'est mal
	// passé.
	renderReport(out, rep, len(todo.withheld))
	if runErr != nil {
		return runErr
	}
	if !rep.Converged() {
		return fmt.Errorf(
			"apply n'a pas convergé : %d ressource(s) retenue(s), %d écart(s)\n"+
				"  → chaque ressource retenue et chaque écart sont détaillés ci-dessus ; "+
				"relancez `notion-seed plan` pour voir ce qui reste",
			len(todo.withheld), len(rep.Mismatches))
	}
	return nil
}

// pending compte ce qu'apply s'apprête à faire, par nature, et ce qu'il retient.
//
// Créations, modifications, destructions et nettoyages sont comptés
// SÉPARÉMENT : retirer une entrée de state obsolète n'écrit rien dans Notion,
// mettre une database à la corbeille, si. Les confondre ferait mentir la phrase
// de confirmation — le seul moment où l'utilisateur décide, sur la foi de ce qui
// est à l'écran. Le nettoyage compte pourtant comme une écriture : c'est une
// identité qu'on jette, et elle ne se retrouve que par un import.
type pending struct {
	create, update, destroy, clean int
	withheld                       []withheldChange
}

// writes dit si quelque chose va être écrit — dans Notion ou dans le state.
func (n pending) writes() bool {
	return n.create+n.update+n.destroy+n.clean > 0
}

// splitByWritability sépare ce qui part vers Notion, ce qui ne touche que le
// state local, et ce qui est RETENU.
//
// Withheld est lu EN PREMIER, comme dans apply.Run : c'est lui, et non Target,
// qui porte l'interdiction d'écrire. La forme des changements autorisés est
// vérifiée par apply.Check, pas ici.
func splitByWritability(p *diff.Plan) pending {
	n := pending{clean: len(p.StaleState)}
	for _, c := range p.Changes {
		if c.Withheld != "" {
			n.withheld = append(n.withheld, withheldChange{resource: c.Resource, reason: c.Withheld})
			continue
		}
		switch c.Kind {
		case resources.KindCreate:
			n.create++
		case resources.KindUpdate:
			n.update++
		case resources.KindDestroy:
			n.destroy++
		}
	}
	return n
}

// withheldChange porte la raison avec le nom : une ressource retenue sans
// motif renverrait l'utilisateur deviner quoi faire.
type withheldChange struct {
	resource string
	reason   string
}

// announceWhatWillHappen dit ce qui va se passer, par nature. Une seule phrase
// qui parlerait d'« écritures » pour tout serait fausse pour le nettoyage, et
// la corbeille a sa propre ligne : c'est la seule qui détruit.
func announceWhatWillHappen(w io.Writer, n pending, parentPageID string) {
	if n.create > 0 {
		fmt.Fprintf(w, "%d database(s) vont être créées dans la page %s.\n",
			n.create, parentPageID)
	}
	if n.update > 0 {
		fmt.Fprintf(w, "%d database(s) vont être modifiées dans Notion.\n", n.update)
	}
	if n.destroy > 0 {
		fmt.Fprintf(w, "%d database(s) vont être mises à la corbeille dans Notion.\n", n.destroy)
	}
	if n.clean > 0 {
		fmt.Fprintf(w, "%d entrée(s) obsolètes vont être retirées du state. "+
			"Rien ne sera écrit dans Notion pour celles-là.\n", n.clean)
	}
}

// renderWithheld affiche les ressources qu'apply refuse d'écrire faute de
// pouvoir l'exprimer. Sa cause est une limite de l'API, pas une limite de cette
// version : attendre une version n'y changera rien.
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

// renderReport dit ce qui est acquis. Le bilan compte le retenu dans son
// propre terme : un apply qui échoue sur une ressource retenue ne doit pas
// afficher un bilan qui laisse croire que tout est passé.
func renderReport(w io.Writer, rep apply.Report, withheld int) {
	for _, l := range rep.Created {
		fmt.Fprintf(w, "+ %s\n", l)
	}
	for _, l := range rep.Updated {
		fmt.Fprintf(w, "~ %s\n", l)
	}
	for _, l := range rep.Destroyed {
		fmt.Fprintf(w, "- %s\n", l)
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
	lines := len(rep.Created) + len(rep.Updated) + len(rep.Destroyed) + len(rep.Cleaned)
	// Les écarts comptent : un apply qui échoue sur une seule corbeille non
	// confirmée doit garder son bilan, qui dit que rien n'a été acquis.
	if lines+withheld+len(rep.Mismatches) == 0 {
		return
	}
	// Une ligne vide sépare le bilan des lignes du rapport, et d'elles seules :
	// la section « Retenu », qui peut le précéder, se termine déjà par une.
	if lines+len(rep.Mismatches) > 0 {
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "Appliqué : %d création(s), %d modification(s), %d mise(s) à la corbeille, "+
		"%d entrée(s) de state nettoyée(s). Retenu : %d ressource(s).\n",
		len(rep.Created), len(rep.Updated), len(rep.Destroyed), len(rep.Cleaned), withheld)
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
		// L'entrée est un terminal (isInteractive a écarté /dev/null), et elle
		// s'est fermée sans un seul octet : c'est un Ctrl-D, ou un terminal
		// refermé. Dire « pas un terminal » ici serait faux, et renverrait vers
		// --auto-approve quelqu'un qui voulait simplement ne pas appliquer.
		//
		// Le retour à la ligne rend la main sous le prompt : sans lui, l'erreur
		// s'imprimerait à la suite de « Confirmez en tapant … : ».
		fmt.Fprintln(cmd.OutOrStdout())
		if errors.Is(err, io.EOF) {
			return false, fmt.Errorf(
				"confirmation interrompue (fin d'entrée) : rien n'a été appliqué\n"+
					"  → relancez la commande et tapez « %s » pour appliquer", confirmWord)
		}
		return false, fmt.Errorf(
			"lecture de la confirmation impossible: %w — rien n'a été appliqué\n"+
				"  → relancez la commande et tapez « %s » pour appliquer", err, confirmWord)
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
			"CI), ou lancez la commande dans un terminal et tapez « %s »", confirmWord)
}
