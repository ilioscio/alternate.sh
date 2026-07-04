{
  description = "alternate.sh — a retro Unix timeshare social network";

  inputs = {
    nixpkgs.url     = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    let
      # NixOS module is system-independent; export it outside eachDefaultSystem.
      nixosModule = import ./nix/module.nix;
    in
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
      in {
        # ── Package ───────────────────────────────────────────────────────────
        packages = {
          default = pkgs.buildGoModule {
            pname   = "alternate-sh";
            version = "0.1.0";
            src     = ./.;

            # Update this hash after any change to go.mod / go.sum:
            #   nix build 2>&1 | grep "got:"   →  copy the sha256 value
            vendorHash = pkgs.lib.fakeHash;

            # Embed the migration files (go:embed requires them at build time).
            # buildGoModule copies src to a temp dir; the embed path resolves fine.

            meta = with pkgs.lib; {
              description  = "A retro Unix timeshare social network";
              homepage     = "https://github.com/ilioscio/alternate.sh";
              license      = licenses.mit;
              maintainers  = [ ];
              mainProgram  = "alternate-sh";
            };
          };

          alternate-sh = self.packages.${system}.default;
        };

        # ── Dev shell ─────────────────────────────────────────────────────────
        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [
            go
            gopls
            gotools      # goimports, staticcheck, etc.
            postgresql_16
          ];
          shellHook = ''
            export GOPATH="$HOME/go"
            export PATH="$GOPATH/bin:$PATH"
            echo "alternate.sh dev shell"
            echo "  go run ./cmd/alternate-sh serve   — start server"
            echo "  go run ./cmd/alternate-sh adduser — create user"
          '';
        };

        # ── Checks (nix flake check) ──────────────────────────────────────────
        checks.build = self.packages.${system}.default;
      }
    ) // {
      # ── NixOS module (system-independent) ────────────────────────────────
      # Consume in another flake:
      #
      #   inputs.alternate-sh.url = "github:ilioscio/alternate.sh";
      #   inputs.alternate-sh.inputs.nixpkgs.follows = "nixpkgs";
      #
      #   modules = [
      #     alternate-sh.nixosModules.default
      #     {
      #       services.alternate-sh = {
      #         enable   = true;
      #         package  = alternate-sh.packages.${system}.default;
      #         hostname = "yournode.example.com";
      #         ssh.port = 22;
      #         web.port = 8080;
      #         nginx.enable = true;
      #         nginx.domain = "yournode.example.com";
      #       };
      #     }
      #   ];
      nixosModules.default = nixosModule;
      nixosModules.alternate-sh = nixosModule;
    };
}
