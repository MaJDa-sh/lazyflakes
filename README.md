<p align="center">
  <img src="assets/logo.svg" alt="lazyflakes logo" width="300">
</p>

<h1 align="center">lazyflakes</h1>

A lazygit-style terminal UI for nixpkgs: search packages from the stable and
unstable channels side by side, then

- drop into a shell with them (`enter`),
- write a `shell.nix` or `flake.nix` (and `.envrc`) for the current project (`w`),
- or install them into your NixOS, nix-darwin or home-manager configuration (`a`).

<p align="center">
  <img src="assets/demo.gif" alt="lazyflakes: searching packages, selecting from both channels, previewing a flake.nix and removing an installed package" width="900">
</p>

## Install

lazyflakes needs [Nix](https://nixos.org/download) (it asks Nix for the
packages). Pick one:

**Try it without installing**

```sh
nix run github:MaJDa-sh/lazyflakes
```

**Install for your user**

```sh
nix profile install github:MaJDa-sh/lazyflakes
```

**In a NixOS, nix-darwin or home-manager flake**

```nix
inputs.lazyflakes.url = "github:MaJDa-sh/lazyflakes";
inputs.lazyflakes.inputs.nixpkgs.follows = "nixpkgs";

# then, in a module:
environment.systemPackages = [ inputs.lazyflakes.packages.${pkgs.stdenv.hostPlatform.system}.default ];
# (or home.packages; overlays.default adds pkgs.lazyflakes instead)
```

**Prebuilt binary** (Linux and macOS, x86_64 and arm64): download the archive
for your system from the [releases page](https://github.com/MaJDa-sh/lazyflakes/releases),
unpack it and put `lazyflakes` somewhere on your `PATH`.

**With Go**: `go install github.com/MaJDa-sh/lazyflakes@latest`, or from a
checkout `./install.sh` (installs to `~/.local/bin`, `PREFIX=…` to change).

> The first start evaluates all of nixpkgs for both channels, which can take
> **1–3 minutes**. The result is cached for a day, so later starts take under
> a second.

## Keys

| Where | Keys |
|---|---|
| Everywhere | `1` Packages · `2` Selected · `3` Installed (or `tab` to cycle) · `q` quit |
| [1] Packages | `/` search · `j/k` move · `space` select · `c` default channel · `enter` shell · `w` write nix file · `a` install into system · `o` options · `ctrl+r` reload |
| [2] Selected | `j/k` move · `space/d` unselect · `c` switch channel · `enter` shell · `w` · `a` · `o` |
| [3] Installed | `j/k` move · `d` mark for removal · `c` change channel · `u` undo · `enter` review and apply · `[`/`]` other target |
| Write popup | `enter` write · `s`/`f` shell.nix / flake.nix · `e` toggle .envrc · `o` options |
| Options | `space` or `1`-`4`: allow unfree, insecure, broken, unsupported |
| Install popup | `enter` apply and rebuild · `s` save without rebuilding · `d` remove · `c` channel · `tab` next target |

Packages lazyflakes installed get a hollow ○ in the package list and are
listed in the **[3] Installed** panel. To remove one, press `3`, move to it,
press `d` (it turns red: "remove on apply"), then `enter` to review the
changes and `enter` again to apply and rebuild.

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

In a flake, list it next to your other modules instead (or use `imports` in
one of them):

```nix
nixosConfigurations.myhost = nixpkgs.lib.nixosSystem {
  system = "x86_64-linux";
  modules = [
    ./hardware-configuration.nix
    ./configuration.nix
    ./lazyflakes.nix   # <- add this
  ];
};
```

then rebuild once. From now on select packages, press `a`, review the
changes and press `enter`. The install popup (and `lazyflakes list`) asks Nix
whether the module really is part of your configuration and warns if not.

If a rebuild fails with `flake … does not provide attribute
'nixosConfigurations."nixos"…'`, the rebuild command in
`~/.config/lazyflakes/config.toml` is missing the name of your configuration.
`nixos-rebuild` then guesses your hostname, which is only right if the flake
calls the configuration the same. Set it explicitly, as you do by hand:
`rebuild = "sudo nixos-rebuild switch --flake /etc/nixos#myhost"`.
`lazyflakes setup` reads the name from your flake and does this for you.

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

### Using your own script instead

If you already have a workflow for adding packages (a particular file, a
particular structure), let your own script do the installing. lazyflakes
then only provides the search, the channel choice and the review, and calls
your script with the result:

```toml
[targets.mine]
kind = "script"
script = "~/bin/add-package"   # a command; extra arguments are appended
```

lazyflakes runs it in your terminal (sudo and prompts work) as

```sh
add-package remove channel:attr [channel:attr ...]   # first, if anything is removed
add-package add    channel:attr [channel:attr ...]   # then, if anything is added
```

for example `add-package add stable:ripgrep unstable:jq`. A package that
switches channel is a `remove` of the old one followed by an `add` of the new
one. These variables are set too:

| Variable | Meaning |
|---|---|
| `LAZYFLAKES_ACTION` | `add` or `remove` |
| `LAZYFLAKES_TARGET` | the target's name, e.g. `mine` |
| `LAZYFLAKES_ALLOW` | options enabled with `o`, comma-separated, e.g. `unfree,insecure` |

Exit with a non-zero status to say it failed: nothing is recorded and the
packages stay as they were. [`examples/install-hook.sh`](examples/install-hook.sh)
is a complete example that keeps one package per line between two marker
comments in a file of yours.

Things to know:

- lazyflakes can't see what your script did, so it records the packages the
  script installed successfully in `~/.local/state/lazyflakes/<target>.json`
  (`file = "…"` changes where). That record drives the ○ marks, the Installed
  panel and the review popup, and it is also how you remove a package later.
  If you change your files by hand, the record doesn't know.
- There is no rebuild, import check or rollback for scripts, because
  lazyflakes can't know what yours does. Rebuild at the end of the script if
  you want to, and make `remove` safe to repeat.
- `lazyflakes add|remove|list` work with script targets too (`-target mine`).

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

The demo GIF is recorded with [VHS](https://github.com/charmbracelet/vhs) in a
throwaway home, so it never touches your system:
`nix-shell -p vhs --run "vhs assets/demo.tape"`.

### Releasing

Releases are built by GoReleaser in GitHub Actions. To publish one:

```sh
git tag v0.1.0
git push origin v0.1.0
```

The workflow runs the tests, builds the binaries and creates the GitHub
release with notes grouped from the conventional commits. Try it locally
first with `nix develop -c goreleaser release --snapshot --clean`.

## License

[MIT](LICENSE). lazyflakes is a community project and is not affiliated with
or endorsed by the NixOS Foundation.
