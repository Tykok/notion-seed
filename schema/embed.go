// Package schema porte le JSON Schema de validation de la configuration.
package schema

import _ "embed"

// Bytes est le JSON Schema embarqué dans le binaire : notion-seed ne dépend
// d'aucun fichier externe pour valider une config.
//
//go:embed notion-seed.schema.json
var Bytes []byte
