// SPDX-License-Identifier: GPL-3.0-or-later

// Package schema holds the JSON Schema that validates the configuration.
package schema

import _ "embed"

// Bytes is the JSON Schema embedded in the binary: notion-seed depends on no
// external file to validate a config.
//
//go:embed notion-seed.schema.json
var Bytes []byte
