# Sourced by assets/demo.tape: a throwaway home with a lazyflakes config and
# two packages "installed" through it, so recording never touches the real
# system. The package cache is shared with the real one to start instantly.
demo=/tmp/lazyflakes-demo
rm -rf "$demo"
mkdir -p "$demo/home/nixos" "$demo/home/project" "$demo/bin"
go build -o "$demo/bin/lazyflakes" .

cat > "$demo/home/nixos/configuration.nix" <<'NIX'
{ imports = [ ./lazyflakes.nix ]; }
NIX
cat > "$demo/home/nixos/lazyflakes.nix" <<'NIX'
{ config, lib, pkgs, ... }:
{
  environment.systemPackages = [
    pkgs.cowsay # lazyflakes:pkg stable cowsay
    pkgs.htop # lazyflakes:pkg stable htop
  ];
}
NIX
mkdir -p "$demo/home/.config/lazyflakes"
cat > "$demo/home/.config/lazyflakes/config.toml" <<TOML
default = "system"
[targets.system]
kind = "nixos"
file = "$demo/home/nixos/lazyflakes.nix"
rebuild = "sudo nixos-rebuild switch"
[targets.system.sets]
stable = "pkgs"
unstable = "pkgs-unstable"
TOML

export XDG_CACHE_HOME="${XDG_CACHE_HOME:-$HOME/.cache}"
export HOME="$demo/home" XDG_CONFIG_HOME="$demo/home/.config"
export PATH="$demo/bin:$PATH" PS1='$ '
cd "$HOME/project"
clear
