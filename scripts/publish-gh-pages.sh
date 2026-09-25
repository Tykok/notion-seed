#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
# Publishes one section of the gh-pages branch and keeps the other.
#
# gh-pages carries two trees: apt/, rebuilt by apt.yml on each release, and the
# documentation site at the root, rebuilt by docs.yml. Each workflow replaces
# its own part only.
#
# Usage: publish-gh-pages.sh apt|site <source-dir> <commit-message>
# Env:   PAGES_REMOTE — URL of the repository to push to.
set -euo pipefail

if [ "$#" -ne 3 ]; then
  echo "usage: $0 apt|site <source-dir> <commit-message>" >&2
  exit 2
fi
section=$1
message=$3
: "${PAGES_REMOTE:?PAGES_REMOTE must name the repository to push to}"

case "$section" in
  apt | site) ;;
  *)
    echo "unknown section: $section (expected apt or site)" >&2
    exit 2
    ;;
esac

source=$(cd "$2" && pwd)
if [ "$section" = site ] && [ -e "$source/apt" ]; then
  echo "the site carries an apt/ directory: it would overwrite the apt repository" >&2
  exit 1
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cd "$work"

git init -q -b gh-pages
git remote add origin "$PAGES_REMOTE"
if git ls-remote --exit-code --heads origin gh-pages >/dev/null 2>&1; then
  # A real checkout, not `reset --soft` into an empty directory: the working
  # tree must hold the other section, otherwise `git add -A` records it as
  # deleted.
  git fetch -q --depth 1 origin gh-pages
  git checkout -q -B gh-pages FETCH_HEAD
fi

if [ "$section" = apt ]; then
  rm -rf apt
  cp -R "$source" apt
else
  find . -mindepth 1 -maxdepth 1 ! -name .git ! -name apt -exec rm -rf {} +
  cp -R "$source"/. .
fi

# Without this, GitHub Pages runs the content through Jekyll, which ignores
# directories starting with an underscore and rewrites some files.
touch .nojekyll

git add -A
if git diff --cached --quiet; then
  echo "nothing to publish"
  exit 0
fi
git -c user.name="github-actions[bot]" \
    -c user.email="41898282+github-actions[bot]@users.noreply.github.com" \
    commit -q -m "$message"
git push -q origin gh-pages
