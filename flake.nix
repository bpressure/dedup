{
  description = "A file deduplication utility";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
      in
      {
        packages.default = pkgs.buildGoModule {
          pname = "dedup";
          version = "0.0.0-src";
          
          src = ./.;
          
          vendorHash = "sha256-aiUoM952HP3RWy9O/vcCftFlj7tt7PE5SddfEtI7vIc=";
          
          ldflags = [ "-s" "-w" "-X main.version=${self.packages.${system}.default.version}" ];
          
          meta = with pkgs.lib; {
            description = "A file deduplication utility";
            homepage = "https://github.com/jpillora/dedup";
            license = licenses.mit;
            maintainers = [ ];
          };
        };
        
        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [
            go
            gotools
            gopls
          ];
        };
      });
}
