package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/google/subcommands"
	"github.com/uji/slack-wait/cmd"
)

// Build information injected via -ldflags by GoReleaser.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// versionInfo returns build information, falling back to the embedded module
// metadata (set by `go install module@version`) when ldflags were not provided.
func versionInfo() (v, c, d string) {
	v, c, d = version, commit, date
	if v != "dev" {
		return v, c, d
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return v, c, d
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		v = info.Main.Version
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			c = s.Value
		case "vcs.time":
			d = s.Value
		}
	}
	return v, c, d
}

func main() {
	if len(os.Args) >= 2 && (os.Args[1] == "version" || os.Args[1] == "--version" || os.Args[1] == "-v") {
		v, c, d := versionInfo()
		fmt.Printf("slack-wait %s (commit %s, built %s)\n", v, c, d)
		os.Exit(0)
	}

	if len(os.Args) >= 2 && (os.Args[1] == "login" || os.Args[1] == "logout") {
		subcommands.Register(&cmd.LoginCommand{}, "")
		subcommands.Register(&cmd.LogoutCommand{}, "")
		flag.Parse()
		os.Exit(int(subcommands.Execute(context.Background())))
	}

	w := &cmd.WaitCommand{}
	f := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	w.SetFlags(f)
	f.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		f.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nSubcommands:\n  login\tauthenticate via browser (PKCE)\n  logout\tremove stored token\n")
	}
	f.Parse(os.Args[1:]) //nolint:errcheck
	os.Exit(int(w.Execute(context.Background(), f)))
}
