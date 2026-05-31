package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/google/subcommands"
	"github.com/uji/slack-wait/internal/auth"
)

// LogoutCommand removes the stored token.
type LogoutCommand struct{}

func (*LogoutCommand) Name() string     { return "logout" }
func (*LogoutCommand) Synopsis() string { return "remove stored token" }
func (*LogoutCommand) Usage() string    { return "logout\n\n" }
func (*LogoutCommand) SetFlags(_ *flag.FlagSet) {}

func (*LogoutCommand) Execute(_ context.Context, _ *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	if err := auth.Delete(); err != nil {
		fmt.Fprintf(os.Stderr, "slack-wait: %v\n", err)
		return subcommands.ExitFailure
	}
	fmt.Fprintln(os.Stderr, "Logged out.")
	return subcommands.ExitSuccess
}
