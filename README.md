# lazyflakes

A lazygit-style terminal UI for nixpkgs: search packages from the stable and
unstable channels side by side, then

- drop into a shell with them (`enter`),
- write a `shell.nix` or `flake.nix` (and `.envrc`) for the current project (`w`),
- or install them into your NixOS, nix-darwin or home-manager configuration (`a`).

## Install

```sh
./install.sh                     # builds and installs to ~/.local/bin
PREFIX=/usr/local ./install.sh   # or somewhere else
```

or `go install github.com/MaJDa-sh/lazyflakes@latest`.

## Keys

| Where | Keys |
|---|---|
| Packages | `/` search · `j/k` move · `space` select · `c` default channel · `enter` shell · `w` write nix file · `a` install into system · `o` options · `tab` selected panel · `ctrl+r` reload · `q` quit |
| Selected | `j/k` move · `space/d` remove · `c` switch channel · `enter` shell · `w` · `a` · `o` |
| Write popup | `enter` write · `s`/`f` shell.nix / flake.nix · `e` toggle .envrc · `o` options |
| Options | `space` or `1`-`4`: allow unfree, insecure, broken, unsupported |
| Install popup | `enter` apply and rebuild · `s` save without rebuilding · `d` remove · `c` channel · `tab` next target |

Flags: `-channel stable|unstable`, `-stable <flake ref>`, `-unstable <flake ref>`,
`-format flake|shell`, `-envrc=false`, `-allow unfree,insecure`, `-config <path>`.
Most have a `LAZYFLAKES_*` environment variable, see `lazyflakes -h`.

The system installs also work without the TUI:

```sh
lazyflakes add ripgrep jq          # install into the default target and rebuild
lazyflakes add -channel unstable -allow unfree vscode
lazyflakes remove jq
lazyflakes list                    # what lazyflakes installed, and whether it is imported
```

(`-target NAME` picks another target, `-no-rebuild` only writes the module.)

## Installing into your system configuration

Every configuration is laid out differently, so lazyflakes does not try to
edit yours. Instead it owns **one module**, `lazyflakes.nix`, which it rewrites
whenever you add or remove packages. You import that module once and the
rest is automatic.

### 1. Run setup

```sh
lazyflakes setup
```

It detects NixOS (flake or `configuration.nix`), nix-darwin and
home-manager, shows the config it proposes, writes it to
`~/.config/lazyflakes/config.toml` and creates an empty `lazyflakes.nix`
(using sudo, doas or run0 if the directory is root-owned). Pressing `a`
without a config offers to run it too.

Flakes are evaluated to find their `nixosConfigurations`,
`darwinConfigurations` and `homeConfigurations`, so flake-parts and similar
layouts are recognised. The configuration named after the machine is used;
`-host NAME` picks another one and `-flake DIR` points setup at a flake
outside the usual places (`/etc/nixos`, `~/nixos-config`, `~/.dotfiles`, …).
With several hosts each gets its own module, e.g. `lazyflakes-laptop.nix`.

### 2. Import the module once

```nix
# /etc/nixos/configuration.nix, or a module of your flake, or home.nix
imports = [ ./lazyflakes.nix ];
```

then rebuild once. From now on select packages, press `a`, review the
changes and press `enter`. The install popup (and `lazyflakes list`) asks Nix
whether the module really is part of your configuration and warns if not.

### What happens on apply

The TUI hands the terminal to a small script (so sudo can ask for your
password) that

1. writes `lazyflakes.nix`, with sudo (or the configured command) only when
   the file is not writable,
2. `git add --intent-to-add`s it if it is new in a git repo (flakes only see tracked files),
3. runs the target's `rebuild` command,
4. and if the rebuild fails, **restores the previous `lazyflakes.nix`**, so a
   bad change never breaks your next rebuild.

### config.toml

```toml
default = "system"
sudo = "sudo"                         # or "doas", "run0": writes root-owned files

[targets.system]
kind = "nixos"                        # nixos, nix-darwin or home-manager
file = "/etc/nixos/lazyflakes.nix"    # the module lazyflakes owns
rebuild = "sudo nixos-rebuild switch" # empty: only write the file
# flake = "/etc/nixos#laptop"         # flake output, lets lazyflakes check the import
# nixpkgs_config = false              # never write nixpkgs.config.* (see below)
# attribute = "environment.systemPackages"  # default for the kind

[targets.system.sets]
stable = "pkgs"
unstable = 'import (fetchTarball "https://github.com/NixOS/nixpkgs/archive/nixpkgs-unstable.tar.gz") { inherit (pkgs) config; system = pkgs.stdenv.hostPlatform.system; }'

[targets.home]
kind = "home-manager"
file = "~/.config/home-manager/lazyflakes.nix"
rebuild = "home-manager switch"
sets = { stable = "pkgs" }
```

`sets` maps each channel to the package set it comes from inside the module:
a module argument (`pkgs`, or anything passed via `specialArgs`) or any Nix
expression. Packages from a channel without a set are refused with a hint;
press `c` in the install popup to take them from another channel instead.

**Flakes** evaluate purely and cannot `fetchTarball`, so pass the other
channel in from your flake:

```nix
inputs.nixpkgs-unstable.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

nixosConfigurations.myhost = nixpkgs.lib.nixosSystem {
  specialArgs.pkgs-unstable = import nixpkgs-unstable {
    system = "x86_64-linux";
    config.allowUnfree = true;
  };
  modules = [ ./configuration.nix ./lazyflakes.nix ];
};
```

and set `unstable = "pkgs-unstable"` under the target's `sets`.

Options enabled with `o` (allow unfree, …) are written into the module as
`nixpkgs.config.* = lib.mkDefault …`, so a value set in your own
configuration still wins. Configurations that bring their own pkgs
(`nixpkgs.pkgs`, `readOnlyPkgs`, home-manager's `useGlobalPkgs`) ignore or
reject `nixpkgs.config`; set `nixpkgs_config = false` for them and allow
unfree in your own configuration instead. Setup does this automatically for
flake hosts that set `nixpkgs.pkgs`.

## Development

```sh
go test ./...                                   # unit tests
LAZYFLAKES_NIX_EVAL=1 go test ./internal/managed  # evaluate generated modules with nix
LAZYFLAKES_E2E=1 go test -run E2E .              # build real NixOS and home-manager configs
LAZYFLAKES_E2E=1 LAZYFLAKES_VM=1 go test -run VirtualMachine .  # boot a NixOS VM (needs KVM)
```

The end-to-end tests drive the `lazyflakes` binary against temporary
configurations and build them with `nixos-rebuild build` / `nix build`; they
use the nixpkgs from `NIX_PATH` and never touch the running system.
