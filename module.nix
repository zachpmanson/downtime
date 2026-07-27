# NixOS module. On a flake-based host:
#
#   imports = [ downtime.nixosModules.default ];
#   services.downtime = {
#     enable = true;
#     openFirewall = true;
#     environmentFile = "/run/secrets/downtime.env";   # holds XMPP_PASSWORD=...
#     settings = {
#       listen = ":8080";
#       monitors = [
#         { name = "Website"; type = "http"; url = "https://example.com"; interval = "30s"; }
#         { name = "Postgres"; type = "tcp"; target = "db.internal:5432"; interval = "60s"; }
#       ];
#       xmpp = {
#         enabled = true;
#         jid = "monitor@example.com";
#         password = "env:XMPP_PASSWORD";   # resolved from environmentFile, kept out of the store
#         recipients = [ "you@example.com" ];
#       };
#     };
#   };
self:
{ config, lib, pkgs, ... }:

let
  cfg = config.services.downtime;
  format = pkgs.formats.json { };

  # Build metadata for the footer, sourced from the flake input's git info.
  sourceInfo = self.sourceInfo or { };
  rev = sourceInfo.shortRev or "dev";
  buildTime = toString (sourceInfo.lastModified or 0);

  # Prefer an out-of-store file (may contain secrets) when given; otherwise
  # render config.json from `settings`. Secrets should use "env:VAR" and come
  # from environmentFile, so the rendered file is safe to keep in the store.
  configFile =
    if cfg.configFile != null
    then cfg.configFile
    else format.generate "downtime-config.json" cfg.settings;

  # Derive the listen port (":8080" or "0.0.0.0:8080" -> 8080) for the firewall.
  listenStr = cfg.settings.listen or ":8080";
  port = lib.toInt (lib.last (lib.splitString ":" listenStr));
in
{
  options.services.downtime = {
    enable = lib.mkEnableOption "downtime uptime monitor";

    package = lib.mkOption {
      type = lib.types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.default.override {
        inherit rev buildTime;
      };
      defaultText = lib.literalExpression "downtime.packages.\${system}.default";
      description = "The downtime package to run.";
    };

    settings = lib.mkOption {
      type = format.type;
      default = { };
      example = lib.literalExpression ''
        {
          listen = ":8080";
          monitors = [
            { name = "Website"; type = "http"; url = "https://example.com"; interval = "30s"; }
          ];
        }
      '';
      description = "Contents of config.json, rendered from Nix. See the project README.";
    };

    configFile = lib.mkOption {
      type = lib.types.nullOr lib.types.path;
      default = null;
      description = ''
        Path to an existing config.json. When set, `settings` is ignored. Use
        this if the config itself must contain secrets not suitable for the
        Nix store (prefer `environmentFile` + "env:VAR" indirection instead).
      '';
    };

    environmentFile = lib.mkOption {
      type = lib.types.nullOr lib.types.path;
      default = null;
      example = "/run/secrets/downtime.env";
      description = ''
        systemd EnvironmentFile holding secrets (e.g. XMPP_PASSWORD=...),
        referenced from settings as "env:XMPP_PASSWORD". Kept out of the store.
      '';
    };

    openFirewall = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "Open the configured listen port in the firewall.";
    };
  };

  config = lib.mkIf cfg.enable {
    systemd.services.downtime = {
      description = "downtime uptime monitor";
      wantedBy = [ "multi-user.target" ];
      after = [ "network-online.target" ];
      wants = [ "network-online.target" ];

      serviceConfig = {
        ExecStart = "${lib.getExe cfg.package} -config ${configFile}";
        Restart = "on-failure";
        RestartSec = 5;

        EnvironmentFile = lib.mkIf (cfg.environmentFile != null) cfg.environmentFile;

        # Runs as a transient unprivileged user; http/tcp checks need no privileges.
        DynamicUser = true;
        NoNewPrivileges = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        PrivateTmp = true;
        PrivateDevices = true;
        ProtectKernelTunables = true;
        ProtectKernelModules = true;
        ProtectControlGroups = true;
        RestrictAddressFamilies = [ "AF_INET" "AF_INET6" ];
        RestrictNamespaces = true;
        LockPersonality = true;
        MemoryDenyWriteExecute = true;
      };
    };

    networking.firewall.allowedTCPPorts = lib.mkIf cfg.openFirewall [ port ];
  };
}
