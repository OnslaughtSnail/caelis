#!/usr/bin/env bash
set -euo pipefail

while IFS= read -r pkg; do
  go test -p 1 -parallel 1 "$pkg"
done < <(go list ./...)
