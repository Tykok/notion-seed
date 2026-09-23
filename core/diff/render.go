// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import (
	"fmt"
	"io"
)

// Render écrit le plan en texte brut. notion-seed est un outil de terminal :
// pas d'interface, pas de couleur inconditionnelle, une sortie qui reste
// lisible dans un pipe et en CI.
//
// L'ordre est délibéré : la dérive d'abord (ce que quelqu'un a fait), le plan
// ensuite (ce qu'on ferait), le hors config après (ce qu'on ne touchera pas),
// le blocage en dernier avec son issue. Chaque section disparaît si elle est
// vide : sans state, la sortie est exactement celle d'avant.
func Render(w io.Writer, p *Plan) error {
	if len(p.Drifts) > 0 {
		if _, err := fmt.Fprintln(w, "Dérive détectée hors de notion-seed"); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		for _, d := range p.Drifts {
			if _, err := fmt.Fprintf(w, "  ~ %s\n", d.Resource); err != nil {
				return err
			}
			for _, line := range d.Lines {
				if _, err := fmt.Fprintf(w, "      %s\n", line); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
	}

	// Un plan bloqué n'est jamais « aucun changement » : une ressource gérée
	// introuvable ou archivée ne produit ni Change ni Unmanaged, seulement une
	// raison de blocage. Sortir ici afficherait la conformité tout en rendant
	// un code d'erreur.
	if len(p.Changes) == 0 && len(p.Unmanaged) == 0 && !p.Blocked {
		_, err := fmt.Fprintln(w, "Aucun changement. La configuration correspond à l'état réel.")
		return err
	}

	if len(p.Changes) > 0 {
		if _, err := fmt.Fprintf(w, "Plan: %d to add, %d to change, %d to destroy\n\n",
			p.ToAdd, p.ToChange, p.ToDestroy); err != nil {
			return err
		}
	}

	for _, c := range p.Changes {
		marker := "~"
		if c.Detail == "(new)" {
			marker = "+"
		}
		if c.Class.Blocking() {
			marker = "x"
		}

		header := fmt.Sprintf("  %s %s", marker, c.Resource)
		if c.Detail != "" {
			header += " " + c.Detail
		}
		if c.Class != ClassSafe {
			header += fmt.Sprintf("  [%s]", c.Class)
		}
		if _, err := fmt.Fprintln(w, header); err != nil {
			return err
		}
		for i, line := range c.Lines {
			suffix := ""
			// Une ligne sûre dans une ressource par ailleurs dangereuse ne doit pas
			// hériter de l'étiquette : c'est la ligne qui porte sa classe.
			if i < len(c.LineClasses) && c.LineClasses[i] != ClassSafe {
				suffix = fmt.Sprintf("  [%s]", c.LineClasses[i])
			}
			if _, err := fmt.Fprintf(w, "      %s%s\n", line, suffix); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}

	if len(p.Unmanaged) > 0 {
		if _, err := fmt.Fprintln(w, "Hors config — présent dans Notion, non touché"); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		for _, u := range p.Unmanaged {
			if _, err := fmt.Fprintf(w, "  %s\n", u.Resource); err != nil {
				return err
			}
			for _, line := range u.Lines {
				if _, err := fmt.Fprintf(w, "      %s\n", line); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
	}

	if p.Blocked {
		if _, err := fmt.Fprintln(w, "Plan bloqué. Rien n'a été appliqué."); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		for _, r := range p.BlockedReasons {
			if _, err := fmt.Fprintf(w, "  %s\n", r); err != nil {
				return err
			}
		}
	}
	return nil
}
