# downtime

A tiny uptime monitor in Go. Single static binary, JSON config, an embedded
status page, and optional XMPP alerts on state changes. No database, no Node,
no browser stack — the whole thing is ~10MB and idles at a few MB of RAM.

## Build

```sh
go build -o downtime .        # plain Go
nix build                     # or via the flake -> ./result/bin/downtime
```

## Run

```sh
cp config.example.json config.json   # edit it
./downtime -config config.json
```

Then open http://localhost:8080.

## Nix

- **Dev shell**: `nix develop` (go + gopls + gotools). With direnv, `direnv allow`
  loads it automatically — the repo ships an `.envrc` (`use flake`).
- **Build**: `nix build` → `./result/bin/downtime`. `nix run` starts it (expects
  `./config.json` in the cwd).
- **Deploy (NixOS)**: import the flake's `nixosModules.default` and configure
  `services.downtime`. It runs the binary as a hardened, `DynamicUser` systemd
  service, renders `config.json` from Nix, opens the firewall port, and pulls
  secrets from an `EnvironmentFile` (kept out of the Nix store).

  ```nix
  # flake.nix inputs: downtime.url = "github:zachpmanson/downtime";
  {
    imports = [ downtime.nixosModules.default ];
    services.downtime = {
      enable = true;
      openFirewall = true;
      environmentFile = "/run/secrets/downtime.env";   # XMPP_PASSWORD=...
      settings = {
        listen = ":8080";
        monitors = [
          { name = "Website"; type = "http"; url = "https://example.com"; interval = "30s"; }
          { name = "Postgres"; type = "tcp"; target = "db.internal:5432"; interval = "60s"; }
        ];
        xmpp = {
          enabled = true;
          jid = "monitor@example.com";
          password = "env:XMPP_PASSWORD";   # resolved from environmentFile
          recipients = [ "you@example.com" ];
        };
      };
    };
  }
  ```

  After changing `go.mod`, refresh `vendorHash` in `package.nix` (set it to
  `lib.fakeHash`, run `nix build`, paste the printed `got:` hash).

## How it works

- One goroutine per monitor checks on its own interval (immediately at startup,
  then every `interval`).
- Results feed an in-memory ring buffer (`history_size` per monitor) that backs
  both the status page and the JSON API.
- A monitor only flips to **down** — and only then fires an alert — after
  `failure_threshold` consecutive failures, which suppresses flapping. A single
  success flips it back to **up** and sends a recovery alert with the total
  downtime.

## Check types

| type   | field    | checks |
|--------|----------|--------|
| `http` | `url`    | GET; status in `expect_status` (default any 2xx/3xx); optional `keyword` must appear in the body |
| `tcp`  | `target` | `host:port` accepts a connection |

Add `"disabled": true` to any monitor to mark it as temporarily decommissioned:
it's shown greyed out on the status page but never probed and never alerts.

## Config

```json
{
  "listen": ":8080",
  "history_size": 100,
  "monitors": [
    { "name": "Website", "type": "http", "url": "https://example.com",
      "interval": "30s", "timeout": "5s", "expect_status": [200] },
    { "name": "Postgres", "type": "tcp", "target": "db.internal:5432",
      "interval": "60s", "timeout": "5s" }
  ],
  "xmpp": {
    "enabled": true,
    "jid": "monitor@example.com",
    "password": "env:XMPP_PASSWORD",
    "server": "",
    "recipients": ["you@example.com"],
    "failure_threshold": 3
  }
}
```

- Durations are Go duration strings (`"30s"`, `"2m"`).
- `password` supports `env:VAR` indirection so the secret stays out of the file.
- `server` is an optional `host:port` XMPP override; if empty it's derived from
  the JID domain on port 5222 (StartTLS).
- With `xmpp.enabled: false`, transitions are still logged to stdout.

## HTTP endpoints

- `GET /` — the embedded status page (polls the API every 10s).
- `GET /api/status` — JSON snapshot of every monitor and its recent history.

## Extending notifications

`notify.Notifier` is a one-method interface (`Notify(Event) error`). Add a
Slack/webhook/email sender by implementing it and wiring it up in `main.go`
alongside the XMPP notifier.
