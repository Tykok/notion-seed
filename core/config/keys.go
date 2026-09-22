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

// DeriveKey construit une key à partir d'un nom affiché : minuscules, accents
// retirés, tout ce qui n'est pas alphanumérique remplacé par un tiret.
func DeriveKey(name string) string {
	folded, _, err := transform.String(
		transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC),
		name)
	if err != nil {
		folded = name
	}

	var b strings.Builder
	lastDash := true // évite un tiret en tête
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
		// Pas de key vide : elle sert d'identité, il faut toujours quelque chose
		// que la résolution de collision puisse suffixer.
		return "resource"
	}
	// Le schéma exige un premier caractère alphanumérique ; le Trim le garantit.
	return key
}

// ResolveKeys remplit les key manquantes et lève les collisions selon la
// stratégie décidée : dérivation du nom, puis préfixe par le nom de la page
// parente, puis suffixe numérique.
//
// Les key explicites sont intouchables : elles sont réservées d'abord, et une
// key dérivée qui les percute est celle qui bouge.
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
