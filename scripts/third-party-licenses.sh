#!/bin/sh
# Writes the license of every module compiled into lazyflakes to one file,
# which release archives ship next to the binary as the MIT and BSD
# licenses of those modules require.
set -eu
out=${1:-THIRD_PARTY_LICENSES.txt}
go mod download
{
  echo "lazyflakes includes the following third-party modules."
  go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}}@{{.Version}} {{.Dir}}{{end}}{{end}}' . |
    sort -u |
    while read -r module dir; do
      for f in "$dir"/LICENSE* "$dir"/LICENCE* "$dir"/COPYING*; do
        [ -f "$f" ] || continue
        printf '\n================================================================\n%s (%s)\n================================================================\n\n' "$module" "$(basename "$f")"
        cat "$f"
      done
    done
} > "$out"
