{
  description = "Consume Meigma release-cli from a Nix flake";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-26.05";
    release = {
      url = "github:meigma/release/v0.1.3";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    { nixpkgs, release, ... }:
    let
      systems = [
        "aarch64-darwin"
        "aarch64-linux"
        "x86_64-darwin"
        "x86_64-linux"
      ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
    in
    {
      packages = forAllSystems (system: {
        release-cli = release.packages.${system}.release-cli;
        default = release.packages.${system}.release-cli;
      });

      apps = forAllSystems (system: {
        release-cli = release.apps.${system}.release-cli;
        default = release.apps.${system}.release-cli;
      });

      devShells = forAllSystems (
        system:
        let
          pkgs = import nixpkgs { inherit system; };
        in
        {
          default = pkgs.mkShellNoCC {
            packages = [ release.packages.${system}.release-cli ];
          };
        }
      );
    };
}
