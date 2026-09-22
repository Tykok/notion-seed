#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
# Échoue si un fichier Go ou shell suivi par git n'annonce pas sa licence.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

missing=0
while IFS= read -r file; do
  [ -f "$file" ] || continue
  if ! head -n 3 "$file" | grep -q 'SPDX-License-Identifier: GPL-3.0-or-later'; then
    echo "en-tête SPDX absent : $file"
    missing=1
  fi
done < <(git ls-files --cached --others --exclude-standard '*.go' '*.sh')

if [ "$missing" -eq 1 ]; then
  echo
  echo "Ajoutez en première ligne : SPDX-License-Identifier: GPL-3.0-or-later"
  exit 1
fi
echo "SPDX : tous les fichiers Go et shell sont couverts."
