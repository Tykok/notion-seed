// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// DeriveKey builds a key from a display name: lowercase, accents removed,
// everything that is not alphanumeric replaced with a dash.
func DeriveKey(name string) string {
	folded, _, err := transform.String(
		transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC),
		name)
	if err != nil {
		folded = name
	}

	var b strings.Builder
	lastDash := true // avoids a leading dash
	for _, r := range strings.ToLower(folded) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	key := strings.Trim(b.String(), "-")
	if key == "" {
		// No empty key: it serves as identity, there must always be something
		// the collision resolution can suffix.
		return "resource"
	}
	// The schema requires an alphanumeric first character; the Trim guarantees it.
	return key
}

// ResolveKeys fills in the missing keys and resolves collisions following the
// chosen strategy: derivation from the name, then a prefix with the parent
// page's name, then a numeric suffix.
//
// Explicit keys are untouchable: they are reserved first, and a derived key
// that collides with them is the one that moves.
func ResolveKeys(dbs []Database, parentName string) []Database {
	out := make([]Database, len(dbs))
	copy(out, dbs)

	taken := make(map[string]bool, len(out))
	for _, db := range out {
		if db.Key != "" {
			taken[db.Key] = true
		}
	}

	prefix := DeriveKey(parentName)
	for i := range out {
		if out[i].Key != "" {
			continue
		}
		base := DeriveKey(out[i].Name)
		out[i].Key = firstFree(base, prefix, taken)
		taken[out[i].Key] = true
	}
	return out
}

func firstFree(base, prefix string, taken map[string]bool) string {
	if !taken[base] {
		return base
	}
	if prefix != "" {
		prefixed := prefix + "-" + base
		if !taken[prefixed] {
			return prefixed
		}
		for n := 2; ; n++ {
			candidate := fmt.Sprintf("%s-%d", prefixed, n)
			if !taken[candidate] {
				return candidate
			}
		}
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d", base, n)
		if !taken[candidate] {
			return candidate
		}
	}
}
