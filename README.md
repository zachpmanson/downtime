# Downtime

HTTP and TCP uptime monitor with a status page, SQLite history, and optional XMPP alerts.

```sh
umask 077
cp config.example.json config.json # edit the monitors
nix run . -- -config config.json    # or: go run . -config config.json
```

Open <http://localhost:8080>. JSON is at `/api/status` or `/api/status/<page>`.
For NixOS, import `nixosModules.default` and set `services.downtime` (see [module.nix](module.nix)).

Keep the database and backups private: failed HTTP checks can store unredacted response headers.
