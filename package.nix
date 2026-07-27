{ lib, buildGoModule }:

buildGoModule {
  pname = "downtime";
  version = "0-unstable";
  src = ./.;

  # vendorHash is the hash of the vendored Go module deps. To refresh after a
  # go.mod change: set this to lib.fakeHash, run `nix build`, and paste the
  # "got: sha256-..." value it prints.
  vendorHash = "sha256-sqeYJ140P0Ke6ZuqBMrzQoBhTNATCzP7MbIVhvenl/k=";

  # Trim the binary; the embedded web assets stay in via go:embed.
  ldflags = [ "-s" "-w" ];

  meta = {
    description = "Tiny uptime monitor with a static status page and XMPP alerts";
    homepage = "https://github.com/zachpmanson/downtime";
    mainProgram = "downtime";
  };
}
