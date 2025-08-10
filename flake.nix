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
          
          vendorHash = "sha256-gESP+A+D65+vaY/1vGQyrY9WmJTZ5Z7exLOytmw/rKI=";
          
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