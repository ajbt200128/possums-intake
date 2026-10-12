{ pkgs, root }:
let
  go = pkgs.go_1_27.overrideAttrs (final: _: {
    version = "1.27.2";
    src = pkgs.fetchurl {
      url = "https://go.dev/dl/go${final.version}.src.tar.gz";
      hash = "sha256-A0ldorpkiU1A9cSZLklFT6eLUGkGBP+Stq//UIG3bmI=";
    };
  });
  buildGoModule = pkgs.buildGoModule.override { inherit go; };
  operatorAssets = builtins.attrNames (builtins.fromJSON (builtins.readFile (root + "/internal/operator/ui/assets.json")));
  operatorInputs = [ "internal/operator/ui/assets.json" ]
    ++ map (name: "internal/operator/ui/dist/" + name) operatorAssets;
  # Only declared build inputs: never private state, .git, env, or documentation.
  src = pkgs.lib.cleanSourceWith {
    src = root;
    filter = path: type:
      let rel = pkgs.lib.removePrefix (toString root + "/") (toString path);
      in type == "directory" && (rel == "cmd" || rel == "internal" || rel == "nix"
        || pkgs.lib.hasPrefix "cmd/" rel || pkgs.lib.hasPrefix "internal/" rel)
        || builtins.elem rel [ "go.mod" "go.sum" "nix/image-smoke.go" ]
        || builtins.elem rel operatorInputs
        || (pkgs.lib.hasSuffix ".go" rel && (pkgs.lib.hasPrefix "cmd/" rel || pkgs.lib.hasPrefix "internal/" rel));
  };
  common = {
    pname = "possums-intake";
    version = "0.1.0-local";
    inherit src;
    vendorHash = "sha256-7P04sWzaA5Mwd58oYvJfwyNm7ME+nhmJn637wXx5t68=";
    env.CGO_ENABLED = "0";
    env.GOTOOLCHAIN = "local";
    # This nixpkgs buildGoModule revision duplicates a top-level GOFLAGS
    # argument into env. Use its supported hook; keep automatic -mod/-trimpath.
    preBuild = ''export GOFLAGS="$GOFLAGS -buildvcs=false"'';
    ldflags = [ "-s" "-w" ]; # buildGoModule adds an empty build ID.
    strictDeps = true;
    doCheck = false;
    subPackages = [ "cmd/intake" ];
  };
  ledger = buildGoModule common;
  # Pure Go cross-compilation permits a local Linux image on macOS. This is
  # NOT evidence of identical native Linux CI builds or production architecture.
  linuxBuild = common // {
    preBuild = common.preBuild + "\n" + ''
      export GOOS=linux
    '';
    postInstall = ''
      if [ -d "$out/bin/linux_$GOARCH" ]; then
        mv "$out/bin/linux_$GOARCH/"* "$out/bin/"
        rmdir "$out/bin/linux_$GOARCH"
      fi
    '';
  };
  linuxLedger = buildGoModule linuxBuild;
  probe = buildGoModule (linuxBuild // {
    pname = "intake-image-smoke-probe";
    subPackages = [ "nix" ];
  });
  image = pkgs.dockerTools.buildLayeredImage {
    name = "possums-intake-local";
    tag = "qualification";
    created = "1970-01-01T00:00:01Z";
    contents = [ linuxLedger pkgs.cacert ];
    extraCommands = ''
      mkdir -p state tmp
      chmod 1777 tmp
      chmod 0700 state
    '';
    fakeRootCommands = ''chown 65532:65532 state'';
    config = {
      Entrypoint = [ "${linuxLedger}/bin/intake" ];
      Cmd = [ "serve" "--state" "/state" "--listen" "127.0.0.1:8090" ];
      User = "65532:65532";
      WorkingDir = "/";
      Env = [ "SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt" "OTEL_SDK_DISABLED=true" ];
    };
  };
in {
  default = ledger;
  ledger-linux = linuxLedger;
  ledger-image = image;
  image-smoke-probe = probe;
  pinned-go = go;
  # Prewarm only external inputs. Fresh CI must refuse already-valid app/image
  # paths, disable substitutes and remote builders, and build independently.
  release-build-deps = (pkgs.linkFarm "intake-release-build-deps" [
    { name = "go"; path = go; }
    { name = "vendor"; path = ledger.goModules; }
    { name = "ca"; path = pkgs.cacert; }
    # Image ownership and Python-check hooks need external tools, not app outputs.
    { name = "fakeroot"; path = pkgs.fakeroot; }
    { name = "dash"; path = pkgs.dash; }
  ]).overrideAttrs (_: {
    passthru.rebuildDerivations = map (drv: drv.drvPath) ([ linuxLedger image
      image.stream image.stream.conf image.stream.conf.layersJsonFile
    ] ++ image.stream.conf.layersJsonFile.drvAttrs.exportReferencesGraph.graph);
  });
}
