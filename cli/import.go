// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tykok/notion-seed/core/config"
	"github.com/tykok/notion-seed/core/preflight"
	"github.com/tykok/notion-seed/core/providers/notion/mapper"
	"github.com/tykok/notion-seed/core/providers/notion/resources"
	"github.com/tykok/notion-seed/core/state"
)

// hex32 reconnaît un id Notion sans tirets, tel qu'il apparaît dans les URL.
var hex32 = regexp.MustCompile(`[0-9a-fA-F]{32}`)

func newImportCmd() *cobra.Command {
	opts := &planOptions{}
	cmd := &cobra.Command{
		Use:   "import database.<key> <id-ou-url>",
		Short: "Adopte une database Notion existante sous une key déclarée",
		Long: "import écrit l'identité et l'état actuel d'une database existante\n" +
			"dans le fichier de state, pour que plan puisse la reconnaître au lieu\n" +
			"de proposer de la recréer.\n\n" +
			"import adopte le réel tel qu'il est : il n'exige pas que la database\n" +
			"corresponde déjà au YAML. C'est le plan qui révélera l'écart ensuite.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runImport(cmd, opts, args[0], args[1])
		},
	}
	opts.bind(cmd)
	// import n'a aucun sens hors ligne : il lit l'état réel. Le flag est masqué
	// de l'aide ET refusé s'il est passé — un flag accepté puis ignoré ferait
	// croire à un import hors ligne qui a en fait appelé l'API.
	_ = cmd.Flags().MarkHidden("skip-preflight")
	return cmd
}

func runImport(cmd *cobra.Command, opts *planOptions, addr, rawID string) error {
	ctx := cmd.Context()

	if err := opts.validate(); err != nil {
		return err
	}
	if cmd.Flags().Changed("skip-preflight") {
		return fmt.Errorf(
			"import n'accepte pas --skip-preflight\n" +
				"  → la commande lit l'état réel de la database pour l'inscrire dans le " +
				"state : elle n'a rien à faire hors ligne")
	}
	key, err := parseResourceAddress(addr)
	if err != nil {
		return err
	}
	id, err := parseNotionID(rawID)
	if err != nil {
		return err
	}

	cfg, err := config.Load(opts.dir)
	if err != nil {
		return err
	}
	desired, err := findDatabase(cfg, key)
	if err != nil {
		return err
	}

	snap, err := state.Load(opts.dir)
	if err != nil {
		return err
	}
	if _, exists := snap.Databases[key]; exists {
		return fmt.Errorf(
			"database.%s est déjà dans %s\n"+
				"  → retirez son entrée de %s pour la ré-importer ; ré-importer sans "+
				"cela écraserait une identité existante",
			key, state.FileName, state.FileName)
	}

	info, err := preflight.Check(ctx, "ntn")
	if err != nil {
		return err
	}
	if err := checkWorkspaceMatch(snap, info.WorkspaceID); err != nil {
		return err
	}

	tr := newTransport(cmd, opts)
	remote, err := resources.NewDatabaseResource(tr, mapper.RemoteDatabaseFromJSON).Read(ctx, id)
	if err != nil {
		return fmt.Errorf(
			"lecture de la database %s impossible: %w\n"+
				"  → vérifiez que l'id désigne bien une database (ouvrez-la en pleine "+
				"page dans Notion : son id est dans l'URL)", id, err)
	}
	rd, ok := remote.(resources.RemoteDatabase)
	if !ok || !rd.Found {
		return fmt.Errorf(
			"database %s introuvable\n"+
				"  → vérifiez l'id ; le jeton de ntn voit tout le workspace sans partage "+
				"préalable, donc ce n'est pas un problème de permissions", id)
	}
	if rd.Archived {
		return fmt.Errorf(
			"database %s est archivée ou en corbeille\n"+
				"  → restaurez-la dans Notion avant de l'importer : planifier contre une "+
				"ressource en corbeille produit des écritures qui échoueront", id)
	}

	adopted, orphans := state.JoinOptionKeys(state.FromRemote(rd), state.FromConfig(*desired))
	if snap.Databases == nil {
		snap.Databases = map[string]state.Database{}
	}
	snap.Databases[key] = adopted
	if snap.WorkspaceID == "" {
		snap.WorkspaceID = info.WorkspaceID
	}
	if err := state.Save(opts.dir, snap); err != nil {
		return err
	}

	cmd.Printf("database.%s importée — id %s (%d propriétés, %d options sans key de config)\n",
		key, rd.ID, len(adopted.Properties), orphans)
	return nil
}

// parseResourceAddress lit une adresse `database.<key>`. Seules les databases
// sont importables au MVP 0 ; le message le dit plutôt que de laisser
// l'utilisateur deviner la forme attendue.
func parseResourceAddress(addr string) (string, error) {
	kind, key, found := strings.Cut(addr, ".")
	if !found || kind != "database" || key == "" {
		return "", fmt.Errorf(
			"adresse %q non reconnue\n"+
				"  → la forme attendue est `database.<key>`, par exemple `database.tasks` ; "+
				"seules les databases sont importables aujourd'hui", addr)
	}
	return key, nil
}

// parseNotionID accepte un UUID, un id sans tirets, ou une URL Notion — c'est
// l'URL que l'utilisateur a réellement sous la main.
func parseNotionID(s string) (string, error) {
	s = strings.TrimSpace(s)

	compact := strings.ReplaceAll(s, "-", "")
	if m := hex32.FindString(compact); m != "" && len(compact) == 32 {
		return dashed(m), nil
	}

	// La query string est retirée AVANT de chercher : une URL de database finit
	// par `?v=<32 hexadécimaux>`, l'id de la VUE. Le garder ferait adopter la
	// vue à la place de la database, avec un id parfaitement bien formé — donc
	// un échec incompréhensible à la lecture, et non au parsing.
	path, _, _ := strings.Cut(s, "?")

	// Dans ce qui reste, l'id est le dernier groupe de 32 hexadécimaux : le
	// titre le précède (`.../Mes-taches-<id>`).
	all := hex32.FindAllString(strings.ReplaceAll(path, "-", ""), -1)
	if len(all) > 0 {
		return dashed(all[len(all)-1]), nil
	}
	return "", fmt.Errorf(
		"identifiant %q non reconnu\n"+
			"  → attendu : un UUID, ou l'URL de la database (ouvrez-la en pleine page "+
			"dans Notion et copiez le lien)", s)
}

func dashed(h string) string {
	h = strings.ToLower(h)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// findDatabase retrouve une database déclarée, et nomme les keys existantes
// quand elle n'existe pas : chercher soi-même le nom exact dans ses fichiers
// est un travail que la commande peut faire.
func findDatabase(cfg *config.Config, key string) (*config.Database, error) {
	keys := make([]string, 0, len(cfg.Databases))
	for i := range cfg.Databases {
		if cfg.Databases[i].Key == key {
			return &cfg.Databases[i], nil
		}
		keys = append(keys, cfg.Databases[i].Key)
	}
	sort.Strings(keys)
	return nil, fmt.Errorf(
		"aucune database de key %q dans la configuration\n"+
			"  → keys déclarées : %s. Déclarez la database dans databases/ avant de "+
			"l'importer : sans déclaration, il n'y a rien à planifier contre elle",
		key, strings.Join(keys, ", "))
}
