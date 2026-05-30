{
  description = "A very basic flake for development shells";
  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };
  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
      {
        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [
            go
            gopls
            git
            air
          ];
        };
      }
    ) // {
      templates.default = {
        path = ./.;
        description = "A very basic flake for development shells";
      };
    };
}
