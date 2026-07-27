package main

import "strconv"

// Build metadata, injected at link time via
//   -ldflags "-X main.commit=<rev> -X main.buildUnix=<unix-seconds>"
// (see package.nix / module.nix, which source them from the flake's
// sourceInfo). Defaults cover `go build` / `go run` dev builds.
var (
	commit    = "dev"
	buildUnix = "0"
)

const repoURL = "https://github.com/zachpmanson/downtime"

func buildUnixInt() int64 {
	n, _ := strconv.ParseInt(buildUnix, 10, 64)
	return n
}
