{
  description = "Meigma release-cli";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-26.05";

  outputs =
    { self, nixpkgs, ... }:
    let
      systems = [
        "aarch64-darwin"
        "aarch64-linux"
        "x86_64-darwin"
        "x86_64-linux"
      ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
      version = (builtins.fromJSON (builtins.readFile ./.release-please-manifest.json)).".";
      commit =
        if self ? rev then
          self.rev
        else if self ? dirtyRev then
          self.dirtyRev
        else
          "unknown";
      packageFor =
        system:
        let
          pkgs = import nixpkgs { inherit system; };
          go = pkgs.go_1_26.overrideAttrs {
            version = "1.26.6";
            src = pkgs.fetchurl {
              url = "https://go.dev/dl/go1.26.6.src.tar.gz";
              hash = "sha256-oHIcVMaIkBRI13rZs+x+p8R0cwdV/4kTgukuy5P/LLE=";
            };
          };
          buildGoModule = pkgs.buildGoModule.override { inherit go; };
        in
        buildGoModule {
          pname = "release-cli";
          inherit version;
          src = self;
          vendorHash = "sha256-88+kLHZjuqsejfXj9RHTIbpCoBIa7i+6GhyMeWbVc2M=";
          subPackages = [ "cmd/release-cli" ];
          env.CGO_ENABLED = "0";
          ldflags = [
            "-s"
            "-w"
            "-X=main.version=${version}"
            "-X=main.commit=${commit}"
          ];
          meta = {
            description = "Validate and publish Meigma release artifacts";
            homepage = "https://github.com/meigma/release";
            mainProgram = "release-cli";
            platforms = systems;
          };
        };
    in
    {
      packages = forAllSystems (
        system:
        let
          package = packageFor system;
        in
        {
          release-cli = package;
          default = package;
        }
      );

      apps = forAllSystems (
        system:
        let
          app = {
            type = "app";
            program = "${self.packages.${system}.release-cli}/bin/release-cli";
            meta.description = "Run release-cli";
          };
        in
        {
          release-cli = app;
          default = app;
        }
      );

      checks = forAllSystems (system: {
        release-cli = self.packages.${system}.release-cli;
      });
    };
}
