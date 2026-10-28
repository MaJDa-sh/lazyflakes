{
  lib,
  buildGoModule,
  version ? "dev",
}:

buildGoModule {
  pname = "lazyflakes";
  inherit version;

  src = lib.fileset.toSource {
    root = ./.;
    fileset = lib.fileset.unions [
      ./go.mod
      ./go.sum
      (lib.fileset.fileFilter (file: file.hasExt "go") ./.)
      ./examples # the tests run the example script
    ];
  };

  vendorHash = "sha256-lIApPbxe7MmpBuTxQiqoan962yHgLHwrkJx7/i2oULc=";

  ldflags = [
    "-s"
    "-w"
    "-X main.version=${version}"
  ];

  meta = {
    description = "Lazygit-style terminal UI for nixpkgs: search, try, write shell.nix/flake.nix and install packages";
    homepage = "https://github.com/MaJDa-sh/lazyflakes";
    license = lib.licenses.mit;
    mainProgram = "lazyflakes";
    platforms = lib.platforms.unix;
  };
}
