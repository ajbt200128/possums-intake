{
  description = "Possums intake: local ledger and disabled delivery artifacts";
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/6510408d147d8c5e6d76043e899e9251756f6a75";

  outputs = { self, nixpkgs }:
    let
      systems = [ "aarch64-darwin" "aarch64-linux" "x86_64-linux" ];
      eachSystem = f: nixpkgs.lib.genAttrs systems (system: f (import nixpkgs {
        inherit system;
        # The author publishes this mirror; keep the exact pinned bytes/version.
        # config.rewriteURL also reaches Darwin's bootstrap fetchurl.
        config.rewriteURL = url:
          if url == "https://www.crufty.net/ftp/pub/sjg/bmake-20260313.tar.gz"
          then "https://ftp.netbsd.org/pub/NetBSD/misc/sjg/bmake-20260313.tar.gz"
          else url;
      }));
      artifacts = pkgs: import ./nix/packages.nix { inherit pkgs; root = ./.; };
    in {
      packages = eachSystem (pkgs: artifacts pkgs);
      checks = eachSystem (pkgs: {
        ledger = (artifacts pkgs).default.overrideAttrs (_: {
          doCheck = true;
          checkPhase = ''
            runHook preCheck
            go test -p 1 -mod=vendor ./cmd/... ./internal/... ./pkg/...
            runHook postCheck
          '';
        });
        delivery = pkgs.runCommand "intake-offline-delivery-tests" {
          nativeBuildInputs = [ pkgs.python3 ];
        } ''
          cp ${./flake.nix} flake.nix
          cp -R ${./scripts} scripts
          chmod -R u+w scripts
          mkdir -p nix
          cp ${./nix/persistent-live-stripe-egress-serve-config.yml.in} nix/persistent-live-stripe-egress-serve-config.yml.in
          mkdir -p .github/workflows
          cp ${./.github/workflows/qualify-synthetic-image.yml} .github/workflows/qualify-synthetic-image.yml
          cp ${./.github/workflows/publish-synthetic-image.yml} .github/workflows/publish-synthetic-image.yml
          cp ${./.github/workflows/publish-synthetic-diagnostic-image.yml} .github/workflows/publish-synthetic-diagnostic-image.yml
          cp ${./.github/workflows/inspect-synthetic-package.yml} .github/workflows/inspect-synthetic-package.yml
          cp ${./.github/workflows/publish-image.yml} .github/workflows/publish-image.yml
          cp ${./.github/workflows/tinfoil-release-publish.yml} .github/workflows/tinfoil-release-publish.yml
          cp ${./.github/workflows/deploy-production.yml} .github/workflows/deploy-production.yml
          PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p 'test_*.py'
          touch $out
        '';
      });
    };
}
