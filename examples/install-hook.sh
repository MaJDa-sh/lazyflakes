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
# Exit non-zero to tell lazyflakes this call did nothing; the calls before
# it, which succeeded, stay recorded.
#
# It also gets `set path=value ...` and `unset path ...` for options chosen
# in the Options tab.
#
# This one keeps one package per line between two marker comments in a file
# of yours, and one option per line between two more, for example
# ~/nixos/packages.nix:
#
#     { pkgs, pkgs-unstable, ... }: {
#       # lazyflakes:settings-begin
#       services.openssh.enable = true;
#       # lazyflakes:settings-end
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

# insert_before puts line $1 before the line containing $2; delete_lines
# deletes the lines starting with $1. The texts reach awk through the
# environment: awk -v would read \" and \\ in them as escapes, which breaks
# Nix strings such as "say \"hi\"".
insert_before() {
  LF_LINE=$1 LF_MARKER=$2 awk 'index($0, ENVIRON["LF_MARKER"]) { print ENVIRON["LF_LINE"] } { print }' "$file" >"$file.tmp"
  mv "$file.tmp" "$file"
}
delete_lines() {
  LF_PREFIX=$1 awk 'index($0, ENVIRON["LF_PREFIX"]) != 1' "$file" >"$file.tmp"
  mv "$file.tmp" "$file"
}

case $action in
  add | remove)
    for spec in "$@"; do
      channel=${spec%%:*}
      attr=${spec#*:}
      case $channel in
        unstable) set=pkgs-unstable ;;
        *) set=pkgs ;;
      esac
      line="    $set.$attr"
      delete_lines "$line"
      [ "$action" = remove ] || insert_before "$line" "# lazyflakes:end"
    done
    ;;
  set | unset)
    # Options arrive as path=value (set) or just the path (unset) and go
    # between two more markers: "# lazyflakes:settings-begin" and "-end".
    for spec in "$@"; do
      path=${spec%%=*}
      delete_lines "    $path = "
      [ "$action" = unset ] || insert_before "    $path = ${spec#*=};" "# lazyflakes:settings-end"
    done
    ;;
  *)
    echo "install-hook.sh: unknown action $action" >&2
    exit 1
    ;;
esac

# Rebuild if you want to, for example:
#   sudo nixos-rebuild switch --flake ~/nixos#myhost
