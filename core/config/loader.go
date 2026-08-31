package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// WorkspaceFile est le fichier de configuration globale, à la racine du
// dossier de config.
const WorkspaceFile = "workspace.yaml"

// DatabasesDir contient un fichier par database, à la manière de Terraform
// chargeant tous les .tf d'un répertoire. Pas de système d'include.
const DatabasesDir = "databases"

// DuplicateKeyError signale deux ressources déclarant la même key. Nomme les
// deux fichiers : sans ça, l'utilisateur doit chercher le doublon à la main.
type DuplicateKeyError struct {
	Key        string
	FirstFile  string
	SecondFile string
}

func (e *DuplicateKeyError) Error() string {
	return fmt.Sprintf(
		"key %q déclarée deux fois : dans %s et dans %s\n"+
			"  → une key est une identité, elle doit être unique sur l'ensemble des "+
			"fichiers : renommez l'une des deux déclarations, ou fusionnez-les",
		e.Key, e.FirstFile, e.SecondFile)
}

// globalSections sont les sections que seul workspace.yaml peut porter. Deux
// d'entre elles décident de quelque chose de sensible : `workspace` choisit la
// cible d'écriture, `lifecycle` porte le garde-fou de destruction.
var globalSections = []string{"version", "workspace", "lifecycle"}

// document est la forme d'un fichier de config individuel. Tous les champs
// sont optionnels : workspace.yaml porte le workspace, les fichiers de
// databases/ portent des databases.
type document struct {
	Version   int        `yaml:"version"`
	Workspace *Workspace `yaml:"workspace"`
	Databases []Database `yaml:"databases"`
	Lifecycle *Lifecycle `yaml:"lifecycle"`
}

// Load charge et fusionne toute la configuration d'un dossier.
//
// L'ordre des passes est impératif : chaque fichier est validé
// individuellement, PUIS l'ensemble est fusionné, PUIS l'unicité globale des
// key est vérifiée. Vérifier l'unicité fichier par fichier laisserait deux
// fichiers déclarer la même key sans qu'aucune validation échoue.
func Load(dir string) (*Config, error) {
	wsPath := filepath.Join(dir, WorkspaceFile)
	if _, err := os.Stat(wsPath); err != nil {
		return nil, fmt.Errorf(
			"%s introuvable dans %s\n"+
				"  → créez-le avec `version: 1` et `workspace.parent_page_id: \"<id de la page parente>\"`, "+
				"ou pointez le bon dossier avec --dir",
			WorkspaceFile, dir)
	}

	paths := []string{wsPath}
	dbPaths, err := yamlFiles(filepath.Join(dir, DatabasesDir))
	if err != nil {
		return nil, err
	}
	paths = append(paths, dbPaths...)

	cfg := &Config{Version: 1}
	// firstSeen retient, pour chaque key, le fichier qui l'a déclarée.
	firstSeen := make(map[string]string)

	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf(
				"lecture de %s impossible: %w\n"+
					"  → %s et les fichiers de databases/ doivent être des fichiers YAML "+
					"lisibles ; si celui-ci est un dossier, renommez-le ou supprimez-le",
				path, err, WorkspaceFile)
		}

		// Les sections présentes sont lues sur une forme LAXISTE, pas sur
		// `document` : une valeur du mauvais type y produirait une erreur de
		// décodage yaml avant qu'on puisse dire quoi que ce soit d'utile, alors
		// que le schéma, lui, sait la nommer.
		var top map[string]any
		if err := yaml.Unmarshal(raw, &top); err != nil {
			return nil, &ValidationError{Path: path, Message: "YAML illisible: " + err.Error()}
		}

		// Un fichier de databases/ ne porte QUE des databases. Démontré :
		// `databases/z.yaml` déclarant `workspace.parent_page_id` détournait la
		// cible d'écriture sans un avertissement, et un `lifecycle: {}` y effaçait
		// prevent_destroy. workspace.yaml étant traité en premier, n'importe quel
		// fichier de databases/ gagnait.
		if path != wsPath {
			if err := rejectGlobalSections(path, top); err != nil {
				return nil, err
			}
		}

		// Pour workspace.yaml, vérifier la version AVANT la validation du schéma,
		// sinon le schéma rejette une version fausse avec son propre message avant
		// qu'on puisse donner notre message plus utile. Tout autre document qui
		// porte `version` a déjà été rejeté ci-dessus, avec son propre message :
		// le message d'énumération générique du schéma ne sort jamais sur ce champ.
		if path == wsPath {
			if err := checkVersion(path, raw, top); err != nil {
				return nil, err
			}
		}

		if err := ValidateDocument(path, raw); err != nil {
			return nil, err
		}
		var doc document
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return nil, &ValidationError{Path: path, Message: "YAML illisible: " + err.Error()}
		}
		if doc.Workspace != nil {
			cfg.Workspace = *doc.Workspace
		}
		if doc.Lifecycle != nil {
			cfg.Lifecycle = *doc.Lifecycle
		}
		for _, db := range doc.Databases {
			db.SourceFile = path
			if err := checkExactlyOneTitle(db); err != nil {
				return nil, err
			}
			cfg.Databases = append(cfg.Databases, db)
		}
	}

	// Unicité des key explicites, sur l'ensemble des fichiers.
	for _, db := range cfg.Databases {
		if db.Key == "" {
			continue
		}
		if first, seen := firstSeen[db.Key]; seen {
			return nil, &DuplicateKeyError{
				Key:        db.Key,
				FirstFile:  first,
				SecondFile: db.SourceFile,
			}
		}
		firstSeen[db.Key] = db.SourceFile
	}

	// Les key manquantes sont dérivées et dédupliquées ensuite, une fois
	// l'ensemble des key explicites connu.
	cfg.Databases = ResolveKeys(cfg.Databases, parentNameHint(cfg))

	// Ordre déterministe : la sortie de plan doit être stable entre deux runs.
	sort.Slice(cfg.Databases, func(i, j int) bool {
		return cfg.Databases[i].Key < cfg.Databases[j].Key
	})
	return cfg, nil
}

// rejectGlobalSections refuse les sections globales dans un fichier autre que
// workspace.yaml. Le fichier ET la section fautive sont nommés : sans ça,
// l'utilisateur ne sait pas lequel de ses fichiers a gagné la fusion.
func rejectGlobalSections(path string, top map[string]any) error {
	var found []string
	for _, section := range globalSections {
		if _, ok := top[section]; ok {
			found = append(found, section)
		}
	}
	if len(found) == 0 {
		return nil
	}
	return &ValidationError{
		Path: path,
		Message: fmt.Sprintf(
			"section %s interdite ici : un fichier de %s/ ne déclare que des databases",
			quotedList(found), DatabasesDir),
		Hint: fmt.Sprintf(
			"déplacez %s dans %s, le seul fichier qui porte la configuration globale — "+
				"sinon `workspace.parent_page_id` y détourne la cible d'écriture et "+
				"`lifecycle` y efface le garde-fou prevent_destroy",
			quotedList(found), WorkspaceFile),
	}
}

// checkVersion exige `version: 1`. La présence du champ est lue sur la forme
// laxiste, sa valeur sur `document` : distinguer « absent » de « présent mais
// faux » change le conseil, et `version: 0` est présent, pas absent.
func checkVersion(path string, raw []byte, top map[string]any) error {
	var doc document
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return &ValidationError{Path: path, Message: "YAML illisible: " + err.Error()}
	}
	value, present := top["version"]
	if present && doc.Version == 1 {
		return nil
	}
	hint := "ajoutez `version: 1` en tête du fichier"
	what := "absent"
	if present {
		hint = fmt.Sprintf("remplacez `version: %v` par `version: 1`", value)
		what = fmt.Sprintf("trouvé %v", value)
	}
	return &ValidationError{
		Path:    path,
		Message: fmt.Sprintf("`version: 1` est obligatoire dans %s (%s)", WorkspaceFile, what),
		Hint:    hint,
	}
}

// checkExactlyOneTitle rejette une database qui ne déclare pas exactement une
// propriété de type title. L'API Notion n'en accepte qu'une par data source, ni
// zéro ni deux ; JSON Schema ne sait pas compter sur `additionalProperties`,
// donc la vérification est ici. Sans elle, `plan` annonce une création que
// l'API refusera certainement — un défaut de la promesse centrale de l'outil,
// pas une fonctionnalité manquante.
func checkExactlyOneTitle(db Database) error {
	var titles []string
	for name, p := range db.Properties {
		if p.Type == "title" {
			titles = append(titles, name)
		}
	}
	sort.Strings(titles)
	switch {
	case len(titles) == 0:
		return &ValidationError{
			Path: db.SourceFile,
			Message: fmt.Sprintf(
				"la database %q ne déclare aucune propriété de type title", db.Name),
			Hint: "l'API Notion en exige exactement une par database — passez une de ses " +
				"propriétés en `type: title`, ou ajoutez `Name: {type: title}`",
		}
	case len(titles) > 1:
		return &ValidationError{
			Path: db.SourceFile,
			Message: fmt.Sprintf(
				"la database %q déclare %d propriétés de type title : %s",
				db.Name, len(titles), quotedList(titles)),
			Hint: "l'API Notion n'en accepte qu'une par database — gardez-en une seule et " +
				"donnez un autre type aux autres (`rich_text` pour du texte libre)",
		}
	}
	return nil
}

// parentNameHint fournit le préfixe de désambiguïsation des key. Au MVP 0 les
// pages ne sont pas des ressources, donc on n'a pas leur nom : on se rabat sur
// l'id de la page parente, tronqué. Quand les pages arriveront (post-MVP),
// remplacer par le nom réel de la page.
func parentNameHint(cfg *Config) string {
	id := cfg.Workspace.ParentPageID
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// yamlFiles liste les *.yaml d'un dossier, hors fichiers cachés. Un dossier
// absent n'est pas une erreur : une config peut n'avoir aucune database.
func yamlFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		if ext := filepath.Ext(name); ext != ".yaml" && ext != ".yml" {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	sort.Strings(out)
	return out, nil
}
