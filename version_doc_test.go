// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/tykok/notion-seed/core/preflight"
)

// La version de ntn exigée est écrite dans le code, dans le README et dans le
// pied de page des releases. Elle y est répétée à la main, donc elle dérive :
// ce test fait échouer la dérive au lieu de la laisser arriver jusqu'à un
// utilisateur qui installe la mauvaise version parce que le README le lui a dit.
func TestDocsAnnoncentLaVersionDeNtnDuCode(t *testing.T) {
	semver := regexp.MustCompile(`\b\d+\.\d+\.\d+\b`)

	for _, path := range []string{"README.md", ".goreleaser.yaml"} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s illisible: %v", path, err)
		}

		var found int
		for i, line := range strings.Split(string(content), "\n") {
			if !strings.Contains(line, "ntn") {
				continue
			}
			for _, version := range semver.FindAllString(line, -1) {
				found++
				if version != preflight.MinNtnVersion {
					t.Errorf("%s:%d annonce ntn %s, le code exige %s\n  %s",
						path, i+1, version, preflight.MinNtnVersion, strings.TrimSpace(line))
				}
			}
		}

		if found == 0 {
			t.Errorf("%s ne mentionne aucune version de ntn : l'utilisateur ne sait pas "+
				"laquelle installer, alors que notion-seed refuse de tourner en dessous de %s",
				path, preflight.MinNtnVersion)
		}
	}
}
