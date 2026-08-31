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
		"key %q déclarée deux fois : dans %s et dans %s — une key est une identité, elle doit être unique sur l'ensemble des fichiers",
		e.Key, e.FirstFile, e.SecondFile)
}

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
			"%s introuvable dans %s — il doit contenir `version: 1` et `workspace.parent_page_id`",
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
			return nil, err
		}

		// Pour workspace.yaml, vérifier la version AVANT la validation du schéma,
		// sinon le schéma rejette une version fausse avec son propre message avant
		// qu'on puisse donner notre message plus utile.
		if path == wsPath {
			var doc document
			if err := yaml.Unmarshal(raw, &doc); err != nil {
				return nil, &ValidationError{Path: path, Message: "YAML illisible: " + err.Error()}
			}
			if doc.Version != 1 {
				hint := "ajoutez `version: 1` en tête du fichier"
				if doc.Version != 0 {
					// Si le champ est présent mais faux, dire de le remplacer
					hint = fmt.Sprintf("remplacez `version: %d` par `version: 1`", doc.Version)
				}
				return nil, &ValidationError{
					Path:    path,
					Message: fmt.Sprintf("`version: 1` est obligatoire dans %s (trouvé %d)", WorkspaceFile, doc.Version),
					Hint:    hint,
				}
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
