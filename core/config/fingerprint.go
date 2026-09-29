// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// Fingerprint is the SHA-256 of the configuration AS UNDERSTOOD, in hex.
//
// It hashes what Load understood, not the bytes of the YAML: a comment, a
// reindentation, a reordered mapping or a database moved to another file does
// not change what notion-seed would do, so it does not change the fingerprint.
// An option key does, since the state joins on it; so does a lifecycle entry,
// since the plan shows it.
//
// The serialization is canonical: fields in a fixed order, properties sorted
// by name (encoding/json sorts map keys), databases sorted by key (Load already
// sorts them, and the sort here does not rely on it), options in their
// declared order — it is visible in Notion —, lifecycle lists sorted and
// deduplicated, since they are sets. An empty list and an absent one mean the
// same thing, and hash the same.
//
// A plan file carries this fingerprint: apply refuses to write a plan
// computed against a configuration that has since changed meaning.
func Fingerprint(cfg *Config) string {
	c := canonicalConfig{
		Version:      cfg.Version,
		ParentPageID: cfg.Workspace.ParentPageID,
		Lifecycle: canonicalLifecycle{
			AcknowledgeDestroy:       sortedSet(cfg.Lifecycle.AcknowledgeDestroy),
			AcknowledgeDataLoss:      sortedSet(cfg.Lifecycle.AcknowledgeDataLoss),
			DeprecatedPreventDestroy: sortedSet(cfg.Lifecycle.DeprecatedPreventDestroy),
			DeprecatedAllowDataLoss:  sortedSet(cfg.Lifecycle.DeprecatedAllowDataLoss),
		},
	}
	for _, db := range cfg.Databases {
		cdb := canonicalDatabase{
			Key:         db.Key,
			Name:        db.Name,
			Description: db.Description,
			Icon:        db.Icon,
		}
		if len(db.Properties) > 0 {
			cdb.Properties = make(map[string]canonicalProperty, len(db.Properties))
			for name, p := range db.Properties {
				cp := canonicalProperty{Type: p.Type, Format: p.Format}
				for _, o := range p.Options {
					cp.Options = append(cp.Options, canonicalOption(o))
				}
				cdb.Properties[name] = cp
			}
		}
		c.Databases = append(c.Databases, cdb)
	}
	sort.Slice(c.Databases, func(i, j int) bool { return c.Databases[i].Key < c.Databases[j].Key })

	// json.Marshal cannot fail on these types: strings, ints, slices and maps
	// with string keys only.
	body, _ := json.Marshal(c)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// The canonical shapes. They are separate from Config on purpose: SourceFile
// and Warnings are not meaning, and a field added to Config later must be
// added here by a deliberate choice, not hashed by accident.
type canonicalConfig struct {
	Version      int                 `json:"version"`
	ParentPageID string              `json:"parent_page_id"`
	Databases    []canonicalDatabase `json:"databases,omitempty"`
	Lifecycle    canonicalLifecycle  `json:"lifecycle"`
}

type canonicalDatabase struct {
	Key         string                       `json:"key"`
	Name        string                       `json:"name,omitempty"`
	Description string                       `json:"description,omitempty"`
	Icon        string                       `json:"icon,omitempty"`
	Properties  map[string]canonicalProperty `json:"properties,omitempty"`
}

type canonicalProperty struct {
	Type    string            `json:"type"`
	Format  string            `json:"format,omitempty"`
	Options []canonicalOption `json:"options,omitempty"`
}

type canonicalOption struct {
	Key   string `json:"key,omitempty"`
	Name  string `json:"name,omitempty"`
	Color string `json:"color,omitempty"`
	Group string `json:"group,omitempty"`
}

type canonicalLifecycle struct {
	AcknowledgeDestroy       []string `json:"acknowledge_destroy,omitempty"`
	AcknowledgeDataLoss      []string `json:"acknowledge_data_loss,omitempty"`
	DeprecatedPreventDestroy []string `json:"prevent_destroy,omitempty"`
	DeprecatedAllowDataLoss  []string `json:"allow_data_loss,omitempty"`
}

// sortedSet returns the distinct values, sorted: a lifecycle list is a set.
func sortedSet(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}
