#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
# Construit un dépôt apt signé à partir des .deb produits par goreleaser.
#
# apt n'a pas d'équivalent de `brew tap` : il n'existe pas de mécanisme par
# utilisateur. Un dépôt apt n'est cependant qu'un arbre de fichiers statiques,
# donc GitHub Pages suffit à l'héberger — c'est ce que ce script produit.
#
# Usage : build-apt-repo.sh <dossier-des-deb> <dossier-de-sortie>
# Variables : APT_SUITE (défaut stable), APT_GPG_KEY (identité de signature).
set -euo pipefail

deb_dir=${1:?usage: build-apt-repo.sh <dossier-des-deb> <dossier-de-sortie>}
out=${2:?usage: build-apt-repo.sh <dossier-des-deb> <dossier-de-sortie>}
suite=${APT_SUITE:-stable}
component=main
origin=notion-seed

for tool in dpkg-deb dpkg-scanpackages apt-ftparchive gpg; do
  command -v "$tool" >/dev/null || { echo "outil absent : $tool" >&2; exit 1; }
done

shopt -s nullglob
debs=("$deb_dir"/*.deb)
[ ${#debs[@]} -gt 0 ] || { echo "aucun .deb dans $deb_dir" >&2; exit 1; }

rm -rf "$out"
mkdir -p "$out/pool/$component"
cp "${debs[@]}" "$out/pool/$component/"

# Les architectures viennent des paquets eux-mêmes : les lister à la main
# produirait un Release qui promet une architecture absente, et apt échoue
# alors sur un 404 au lieu de simplement ignorer.
archs=$(for f in "$out/pool/$component"/*.deb; do dpkg-deb -f "$f" Architecture; done | sort -u)

for arch in $archs; do
  dir="$out/dists/$suite/$component/binary-$arch"
  mkdir -p "$dir"
  # --multiversion, sans quoi dpkg-scanpackages ne garde QUE la version la plus
  # récente de chaque paquet : `apt install notion-seed=0.1.0` échouerait dès la
  # publication de la 0.2.0, et une release ne serait plus jamais réinstallable.
  # Exécuté depuis $out pour que le champ Filename soit relatif à la racine du
  # dépôt : un chemin absolu ici renverrait apt vers le disque du build.
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

# InRelease (signature en ligne) ET Release.gpg (signature détachée) : apt
# moderne ne lit que InRelease, mais les images plus anciennes encore en
# service ne cherchent que Release.gpg.
key=${APT_GPG_KEY:-}
gpg_args=(--batch --yes)
[ -n "$key" ] && gpg_args+=(--local-user "$key")
gpg "${gpg_args[@]}" --clearsign -o "$out/dists/$suite/InRelease" "$out/dists/$suite/Release"
gpg "${gpg_args[@]}" --detach-sign --armor -o "$out/dists/$suite/Release.gpg" "$out/dists/$suite/Release"
gpg --armor --export ${key:+"$key"} > "$out/gpg.key"

echo "dépôt apt construit dans $out"
echo "  suite         : $suite"
echo "  architectures : $(echo "$archs" | tr '\n' ' ')"
echo "  paquets       : ${#debs[@]}"
