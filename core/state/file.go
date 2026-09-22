// SPDX-License-Identifier: GPL-3.0-or-later

package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Load lit le fichier de state d'un dossier de configuration.
//
// Un fichier absent n'est pas une erreur : c'est l'état de départ de tout
// projet, et c'est ce qui fait que `plan` sans state se comporte exactement
// comme avant l'existence de ce paquet.
func Load(dir string) (*Snapshot, error) {
	path := Path(dir)
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Snapshot{Version: Version}, nil
	}
	if err != nil {
		return nil, fmt.Errorf(
			"lecture de %s impossible: %w\n"+
				"  → vérifiez les droits sur le fichier, ou retirez-le pour repartir "+
				"d'un state vide (les ressources devront être ré-importées)", path, err)
	}

	var s Snapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf(
			"%s illisible: %w\n"+
				"  → le fichier est tronqué ou mal fusionné. Restaurez-le depuis git "+
				"(`git checkout -- %s`) plutôt que de le supprimer : le supprimer ferait "+
				"recréer les databases au lieu de les reconnaître",
			FileName, err, FileName)
	}
	if s.Version != Version {
		return nil, fmt.Errorf(
			"%s est en version %d, cette version de notion-seed lit la version %d\n"+
				"  → mettez notion-seed à jour ; ne modifiez pas le fichier à la main, "+
				"ses identités seraient fausses",
			FileName, s.Version, Version)
	}
	if s.Databases == nil {
		s.Databases = map[string]Database{}
	}
	return &s, nil
}

// Save écrit le state de façon atomique et déterministe.
//
// Atomique : un state tronqué par une interruption est une identité perdue,
// donc une database recréée en double au premier apply. On écrit un fichier
// temporaire dans le MÊME dossier (pour que rename ne traverse pas de système
// de fichiers), on le synchronise, puis on le renomme.
//
// Déterministe : json.Marshal trie les clés de map, l'indentation est fixe et
// le fichier se termine par un newline. Un fichier versionné dont l'ordre bouge
// à chaque écriture est illisible en revue.
func Save(dir string, s *Snapshot) error {
	s.Version = Version

	body, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("sérialisation du state impossible: %w", err)
	}
	body = append(body, '\n')

	tmp, err := os.CreateTemp(dir, ".notion-seed.state.*.json")
	if err != nil {
		return fmt.Errorf(
			"écriture de %s impossible: %w\n"+
				"  → vérifiez les droits d'écriture sur %s ; l'ancien state est intact",
			FileName, err, dir)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op après un rename réussi

	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return fmt.Errorf("écriture de %s impossible: %w\n"+
			"  → l'ancien state est intact", FileName, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("synchronisation de %s impossible: %w\n"+
			"  → l'ancien state est intact", FileName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("fermeture de %s impossible: %w\n"+
			"  → l'ancien state est intact", FileName, err)
	}
	if err := os.Rename(tmpName, filepath.Join(dir, FileName)); err != nil {
		return fmt.Errorf("remplacement de %s impossible: %w\n"+
			"  → l'ancien state est intact", FileName, err)
	}
	return nil
}
