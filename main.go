package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/google/subcommands"
	"github.com/uji/slack-wait/cmd"
)

// Build information injected via -ldflags by GoReleaser.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) >= 2 && (os.Args[1] == "version" || os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Printf("slack-wait %s (commit %s, built %s)\n", version, commit, date)
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
