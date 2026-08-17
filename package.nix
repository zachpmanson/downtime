# rev/buildTime are injected by module.nix from the flake's sourceInfo (commit
# short-rev and last-modified unix time) so the status page footer can show the
# deployed commit + date. They default to dev values for a plain `nix build`.
{ lib, buildGoModule, rev ? "dev", buildTime ? "0" }:

buildGoModule {
  pname = "downtime";
  version = "0-unstable";
  src = ./.;

  # vendorHash is the hash of the vendored Go module deps. To refresh after a
  # go.mod change: set this to lib.fakeHash, run `nix build`, and paste the
  # "got: sha256-..." value it prints.
  vendorHash = "sha256-On2LOamGllPe4KnFU0Vm8DJBvCNGrNTn9HnL/3Rcsgk=";

  # Trim the binary (the embedded web assets stay in via go:embed) and stamp in
  # the build metadata read by version.go.
  ldflags = [
    "-s"
    "-w"
    "-X main.commit=${rev}"
    "-X main.buildUnix=${buildTime}"
  ];

  meta = {
    description = "Tiny uptime monitor with a static status page and XMPP alerts";
    homepage = "https://github.com/zachpmanson/downtime";
    mainProgram = "downtime";
  };
}
