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
          version = "0.5.0";
          
          src = ./.;
          
          vendorHash = "sha256-urKK/87xaDc407HVoYGJoWalnKWNhPf5nF2ybC9gdBQ=";
          
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
