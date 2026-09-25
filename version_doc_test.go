// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/tykok/notion-seed/core/preflight"
)

// The required ntn version is written in the code, in the README and in the
// release footer. It is repeated by hand, so it drifts: this test fails on the
// drift instead of letting it reach a user who installs the wrong version
// because the README told them to.
func TestDocsAnnounceTheNtnVersionOfTheCode(t *testing.T) {
	semver := regexp.MustCompile(`\b\d+\.\d+\.\d+\b`)

	for _, path := range []string{"README.md", ".goreleaser.yaml"} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s unreadable: %v", path, err)
		}

		var found int
		for i, line := range strings.Split(string(content), "\n") {
			if !strings.Contains(line, "ntn") {
				continue
			}
			for _, version := range semver.FindAllString(line, -1) {
				found++
				if version != preflight.MinNtnVersion {
					t.Errorf("%s:%d announces ntn %s, the code requires %s\n  %s",
						path, i+1, version, preflight.MinNtnVersion, strings.TrimSpace(line))
				}
			}
		}

		if found == 0 {
			t.Errorf("%s mentions no ntn version: the user does not know "+
				"which one to install, while notion-seed refuses to run below %s",
				path, preflight.MinNtnVersion)
		}
	}
}
