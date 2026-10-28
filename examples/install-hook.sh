#!/bin/sh
# An example install script for lazyflakes (kind = "script").
#
# lazyflakes calls it as
#
#     install-hook.sh add    channel:attr [channel:attr ...]
#     install-hook.sh remove channel:attr [channel:attr ...]
#
# and sets LAZYFLAKES_ACTION (add or remove), LAZYFLAKES_TARGET (the target's
# name) and LAZYFLAKES_ALLOW (enabled options, comma-separated, e.g.
# "unfree,insecure"). It runs in your terminal, so sudo and prompts work.
# Exit non-zero to tell lazyflakes nothing was installed.
#
# This one keeps one package per line between two marker comments in a file
# of yours, for example ~/nixos/packages.nix:
#
#     { pkgs, pkgs-unstable, ... }: {
#       environment.systemPackages = [
#         # lazyflakes:begin
#         pkgs.ripgrep
#         pkgs-unstable.jq
#         # lazyflakes:end
#       ];
#     }
#
# Adapt it to your own layout, and rebuild at the end if you like.
set -eu

file=${PACKAGES_FILE:-$HOME/nixos/packages.nix}
action=$1
shift

for spec in "$@"; do
  channel=${spec%%:*}
  attr=${spec#*:}
  case $channel in
    unstable) set=pkgs-unstable ;;
    *) set=pkgs ;;
  esac
  line="    $set.$attr"

  case $action in
    add)
      # Skip it when it is already there, otherwise insert before the end marker.
      grep -qxF "$line" "$file" ||
        awk -v line="$line" '/# lazyflakes:end/ { print line } { print }' "$file" >"$file.tmp"
      [ ! -e "$file.tmp" ] || mv "$file.tmp" "$file"
      ;;
    remove)
      grep -vxF "$line" "$file" >"$file.tmp" || true
      mv "$file.tmp" "$file"
      ;;
  esac
done

# Rebuild if you want to, for example:
#   sudo nixos-rebuild switch --flake ~/nixos#myhost
