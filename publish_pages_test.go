// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// gh-pages carries two trees published by two workflows: apt/ on each release,
// the documentation site at the root on each push to main. Each publication
// must replace its own part and leave the other one alone. The apt workflow
// used to stage everything outside apt/ as deleted, so the first release after
// the site went live would have taken it down.

func newPagesRemote(t *testing.T) string {
	t.Helper()
	for _, tool := range []string{"bash", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not found", tool)
		}
	}
	remote := filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", remote).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	return remote
}

func publishPages(t *testing.T, remote, section string, files map[string]string) error {
	t.Helper()
	src := t.TempDir()
	for name, content := range files {
		path := filepath.Join(src, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("bash", "scripts/publish-gh-pages.sh", section, src, "test: "+section)
	cmd.Env = append(os.Environ(), "PAGES_REMOTE="+remote)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v\n%s", err, out)
	}
	return nil
}

func mustPublishPages(t *testing.T, remote, section string, files map[string]string) {
	t.Helper()
	if err := publishPages(t, remote, section, files); err != nil {
		t.Fatalf("publish %s: %v", section, err)
	}
}

// installHook writes a pre-receive hook in the bare remote. The hook decides
// whether to accept the push about to land on gh-pages, which is how these
// tests simulate a push raced by a concurrent publication without actually
// running two workflows at once.
func installHook(t *testing.T, remote, script string) {
	t.Helper()
	path := filepath.Join(remote, "hooks", "pre-receive")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// rejectOnceHook rejects exactly the first push it sees — as if another
// publication had pushed gh-pages first — then accepts every push after that,
// once the caller has re-fetched and retried.
const rejectOnceHook = `#!/usr/bin/env bash
marker="$(pwd)/reject-once.done"
if [ ! -e "$marker" ]; then
  touch "$marker"
  echo "pre-receive: rejecting on purpose (test)" >&2
  exit 1
fi
exit 0
`

// alwaysRejectHook rejects every push, as if gh-pages could never be updated.
const alwaysRejectHook = `#!/usr/bin/env bash
echo "pre-receive: rejecting on purpose (test)" >&2
exit 1
`

func pagesTree(t *testing.T, remote string) []string {
	t.Helper()
	out, err := exec.Command("git", "--git-dir", remote, "ls-tree", "-r", "--name-only", "gh-pages").Output()
	if err != nil {
		return nil
	}
	return strings.Fields(string(out))
}

func pagesCommits(t *testing.T, remote string) int {
	t.Helper()
	out, err := exec.Command("git", "--git-dir", remote, "rev-list", "--count", "gh-pages").Output()
	if err != nil {
		t.Fatalf("rev-list: %v", err)
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return n
}

func assertPagesTree(t *testing.T, remote string, want ...string) {
	t.Helper()
	if got := pagesTree(t, remote); !slices.Equal(got, want) {
		t.Errorf("gh-pages holds %v, want %v", got, want)
	}
}

func TestPublishingTheSiteKeepsTheAptRepository(t *testing.T) {
	remote := newPagesRemote(t)
	mustPublishPages(t, remote, "apt", map[string]string{"gpg.key": "key"})
	mustPublishPages(t, remote, "site", map[string]string{"index.html": "home"})

	assertPagesTree(t, remote, ".nojekyll", "apt/gpg.key", "index.html")
}

func TestPublishingAptKeepsTheSite(t *testing.T) {
	remote := newPagesRemote(t)
	mustPublishPages(t, remote, "site", map[string]string{"index.html": "home", "fr/index.html": "accueil"})
	mustPublishPages(t, remote, "apt", map[string]string{"gpg.key": "key"})

	assertPagesTree(t, remote, ".nojekyll", "apt/gpg.key", "fr/index.html", "index.html")
}

func TestPublishingTheSiteRemovesPagesItNoLongerHas(t *testing.T) {
	remote := newPagesRemote(t)
	mustPublishPages(t, remote, "apt", map[string]string{"gpg.key": "key"})
	mustPublishPages(t, remote, "site", map[string]string{"index.html": "home", "old.html": "old"})
	mustPublishPages(t, remote, "site", map[string]string{"index.html": "home"})

	assertPagesTree(t, remote, ".nojekyll", "apt/gpg.key", "index.html")
}

func TestPublishingAptReplacesTheWholeAptTree(t *testing.T) {
	remote := newPagesRemote(t)
	mustPublishPages(t, remote, "apt", map[string]string{"pool/a.deb": "a"})
	mustPublishPages(t, remote, "apt", map[string]string{"pool/b.deb": "b"})

	assertPagesTree(t, remote, ".nojekyll", "apt/pool/b.deb")
}

func TestPublishingNothingNewCreatesNoCommit(t *testing.T) {
	remote := newPagesRemote(t)
	mustPublishPages(t, remote, "site", map[string]string{"index.html": "home"})
	before := pagesCommits(t, remote)
	mustPublishPages(t, remote, "site", map[string]string{"index.html": "home"})

	if after := pagesCommits(t, remote); after != before {
		t.Errorf("an identical publication created %d commit(s)", after-before)
	}
}

func TestASiteThatShipsAnAptDirectoryIsRefused(t *testing.T) {
	remote := newPagesRemote(t)
	mustPublishPages(t, remote, "apt", map[string]string{"gpg.key": "key"})

	if err := publishPages(t, remote, "site", map[string]string{"apt/gpg.key": "forged"}); err == nil {
		t.Fatal("a site carrying apt/ was published: it would overwrite the apt repository")
	}
	assertPagesTree(t, remote, ".nojekyll", "apt/gpg.key")
}

func TestPublishRetriesAndSucceedsAfterARejectedPush(t *testing.T) {
	remote := newPagesRemote(t)
	mustPublishPages(t, remote, "apt", map[string]string{"gpg.key": "key"})
	installHook(t, remote, rejectOnceHook)

	if err := publishPages(t, remote, "site", map[string]string{"index.html": "home"}); err != nil {
		t.Fatalf("publish site: %v", err)
	}

	assertPagesTree(t, remote, ".nojekyll", "apt/gpg.key", "index.html")
}

func TestPublishFailsWhenThePushIsAlwaysRejected(t *testing.T) {
	remote := newPagesRemote(t)
	mustPublishPages(t, remote, "apt", map[string]string{"gpg.key": "key"})
	installHook(t, remote, alwaysRejectHook)

	if err := publishPages(t, remote, "site", map[string]string{"index.html": "home"}); err == nil {
		t.Fatal("publish succeeded despite a pre-receive hook rejecting every push")
	}

	assertPagesTree(t, remote, ".nojekyll", "apt/gpg.key")
}

func TestAnUnknownSectionIsRefused(t *testing.T) {
	remote := newPagesRemote(t)
	if err := publishPages(t, remote, "docs", map[string]string{"index.html": "home"}); err == nil {
		t.Fatal("an unknown section was accepted")
	}
	if tree := pagesTree(t, remote); tree != nil {
		t.Errorf("gh-pages was created: %v", tree)
	}
}
