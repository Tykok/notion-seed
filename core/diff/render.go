// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import (
	"fmt"
	"io"
)

// Render écrit le plan en texte brut. notion-seed est un outil de terminal :
// pas d'interface, pas de couleur inconditionnelle, une sortie qui reste
// lisible dans un pipe et en CI.
func Render(w io.Writer, p *Plan) error {
	if len(p.Changes) == 0 {
		_, err := fmt.Fprintln(w, "Aucun changement. La configuration correspond à l'état réel.")
		return err
	}

	if _, err := fmt.Fprintf(w, "Plan: %d to add, %d to change, %d to destroy\n\n",
		p.ToAdd, p.ToChange, p.ToDestroy); err != nil {
		return err
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
		for _, line := range c.Lines {
			if _, err := fmt.Fprintf(w, "      %s\n", line); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}

	if p.Blocked {
		if _, err := fmt.Fprintln(w,
			"Plan bloqué : au moins un changement est refusé par défaut. "+
				"Rien n'a été appliqué."); err != nil {
			return err
		}
	}
	return nil
}
