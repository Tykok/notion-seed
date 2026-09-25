#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
# Builds a signed apt repository from the .deb files produced by goreleaser.
#
# apt has no equivalent of `brew tap`: there is no per-user mechanism. An apt
# repository is however just a tree of static files, so GitHub Pages is
# enough to host it — that is what this script produces.
#
# Usage: build-apt-repo.sh <deb-dir> <output-dir>
# Variables: APT_SUITE (default stable), APT_GPG_KEY (signing identity).
set -euo pipefail

deb_dir=${1:?usage: build-apt-repo.sh <deb-dir> <output-dir>}
out=${2:?usage: build-apt-repo.sh <deb-dir> <output-dir>}
suite=${APT_SUITE:-stable}
component=main
origin=notion-seed

for tool in dpkg-deb dpkg-scanpackages apt-ftparchive gpg; do
  command -v "$tool" >/dev/null || { echo "missing tool: $tool" >&2; exit 1; }
done

shopt -s nullglob
debs=("$deb_dir"/*.deb)
[ ${#debs[@]} -gt 0 ] || { echo "no .deb in $deb_dir" >&2; exit 1; }

rm -rf "$out"
mkdir -p "$out/pool/$component"
cp "${debs[@]}" "$out/pool/$component/"

# The architectures come from the packages themselves: listing them by hand
# would produce a Release promising a missing architecture, and apt then fails
# on a 404 instead of simply ignoring it.
archs=$(for f in "$out/pool/$component"/*.deb; do dpkg-deb -f "$f" Architecture; done | sort -u)

for arch in $archs; do
  dir="$out/dists/$suite/$component/binary-$arch"
  mkdir -p "$dir"
  # --multiversion, without which dpkg-scanpackages keeps ONLY the most recent
  # version of each package: `apt install notion-seed=0.1.0` would fail as soon
  # as 0.2.0 is published, and a release could never be reinstalled again.
  # Run from $out so that the Filename field is relative to the repository
  # root: an absolute path here would send apt to the build machine's disk.
  (cd "$out" && dpkg-scanpackages --multiversion --arch "$arch" "pool/$component") > "$dir/Packages"
  gzip -9nc "$dir/Packages" > "$dir/Packages.gz"
done

(
  cd "$out/dists/$suite"
  apt-ftparchive \
    -o APT::FTPArchive::Release::Origin="$origin" \
    -o APT::FTPArchive::Release::Label="$origin" \
    -o APT::FTPArchive::Release::Suite="$suite" \
    -o APT::FTPArchive::Release::Codename="$suite" \
    -o APT::FTPArchive::Release::Components="$component" \
    -o APT::FTPArchive::Release::Architectures="$(echo "$archs" | tr '\n' ' ')" \
    release . > Release
)

# InRelease (inline signature) AND Release.gpg (detached signature): modern apt
# reads only InRelease, but older images still in service look only for
# Release.gpg.
key=${APT_GPG_KEY:-}
gpg_args=(--batch --yes)
[ -n "$key" ] && gpg_args+=(--local-user "$key")
gpg "${gpg_args[@]}" --clearsign -o "$out/dists/$suite/InRelease" "$out/dists/$suite/Release"
gpg "${gpg_args[@]}" --detach-sign --armor -o "$out/dists/$suite/Release.gpg" "$out/dists/$suite/Release"
gpg --armor --export ${key:+"$key"} > "$out/gpg.key"

echo "apt repository built in $out"
echo "  suite         : $suite"
echo "  architectures : $(echo "$archs" | tr '\n' ' ')"
echo "  packages      : ${#debs[@]}"
