{
  description = "Agent-ergonomic AxeOS miner CLI";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    devenv.url = "github:cachix/devenv";
    devenv.inputs.nixpkgs.follows = "nixpkgs";
  };

  outputs = { nixpkgs, devenv, ... }@inputs:
    let
      systems = [
        "aarch64-darwin"
        "aarch64-linux"
        "x86_64-darwin"
        "x86_64-linux"
      ];
      version = "0.3.0";
    in
    {
      packages = nixpkgs.lib.genAttrs systems (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
          axeos-axi = pkgs.buildGoModule {
            pname = "axeos-axi";
            inherit version;
            src = ./.;
            subPackages = [ "cmd/axeos-axi" ];
            vendorHash = null;
            ldflags = [ "-X main.version=v${version}" ];
            meta = {
              description = "Agent-ergonomic CLI for reading and tuning AxeOS Bitcoin miners";
              homepage = "https://github.com/Azd325/axeos-axi";
              license = pkgs.lib.licenses.mit;
              mainProgram = "axeos-axi";
            };
          };
        in
        {
          inherit axeos-axi;
          default = axeos-axi;
        });

      devShells = nixpkgs.lib.genAttrs systems (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
        in
        {
          default = devenv.lib.mkShell {
            inherit inputs pkgs;
            modules = [ ./devenv.nix ];
          };
        });
    };
}
