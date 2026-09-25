#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
# Fails if a Go or shell file tracked by git does not declare its license.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

missing=0
while IFS= read -r file; do
  [ -f "$file" ] || continue
  if ! head -n 3 "$file" | grep -q 'SPDX-License-Identifier: GPL-3.0-or-later'; then
    echo "missing SPDX header: $file"
    missing=1
  fi
done < <(git ls-files --cached --others --exclude-standard '*.go' '*.sh')

if [ "$missing" -eq 1 ]; then
  echo
  echo "Add as the first line: SPDX-License-Identifier: GPL-3.0-or-later"
  exit 1
fi
echo "SPDX: every Go and shell file is covered."
