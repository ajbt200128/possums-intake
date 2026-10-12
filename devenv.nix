{ pkgs, config, ... }:

let
  # Go 1.27 is required by the pinned PocketBase v0.40.5. `pkgs.go_1_27` comes
  # from the exact nixpkgs revision pinned in devenv.yaml.
  #
  # devenv's own `languages.go` module is intentionally NOT enabled here: it
  # additionally builds editor tooling (go-tools/gopls/delve) that currently
  # fails to compile against Go 1.27 in this nixpkgs revision. The toolchain
  # package and its environment contract are set explicitly instead.
  go = pkgs.go_1_27;
in
{
  packages = [
    go
    pkgs.git
    pkgs.curl
  ];

  # Match the environment contract devenv's Go module would otherwise provide.
  env.GOROOT = "${go}/share/go";
  env.GOPATH = config.env.DEVENV_STATE + "/go";
  env.GOTOOLCHAIN = "local";

  enterShell = ''
    export PATH="$GOPATH/bin:$PATH"
  '';
}
