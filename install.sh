#!/usr/bin/env bash
# Build lazyflakes and install it into a directory on your PATH.
#
#   ./install.sh                 # installs to ~/.local/bin
#   PREFIX=/usr/local ./install.sh   # installs to /usr/local/bin (may need sudo)
set -euo pipefail

cd "$(dirname "$0")"
bindir="${PREFIX:-$HOME/.local}/bin"

build() {
  if command -v go >/dev/null 2>&1; then
    go build -trimpath -ldflags "-s -w" -o "$1" .
  elif command -v nix-shell >/dev/null 2>&1; then
    echo "go not found, building inside nix-shell -p go"
    nix-shell -p go --run "go build -trimpath -ldflags '-s -w' -o '$1' ."
  else
    echo "error: need either go or nix to build lazyflakes" >&2
    exit 1
  fi
}

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
echo "building lazyflakes…"
build "$tmp/lazyflakes"

mkdir -p "$bindir"
install -m 0755 "$tmp/lazyflakes" "$bindir/lazyflakes"
echo "installed $bindir/lazyflakes"

case ":$PATH:" in
  *":$bindir:"*) echo "run it from any project directory: lazyflakes" ;;
  *)
    echo
    echo "$bindir is not on your PATH. Add this to your shell config (~/.bashrc, ~/.zshrc):"
    echo "  export PATH=\"$bindir:\$PATH\""
    echo "On NixOS with home-manager you can instead set: home.sessionPath = [ \"$bindir\" ];"
    ;;
esac
