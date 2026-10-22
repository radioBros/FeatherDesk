// SPDX-License-Identifier: AGPL-3.0-only

// Command featherdesk is the whole product in one static binary: HTTP API,
// WebSocket hub, in-process queue, embedded SPA and the admin subcommands
// (distroless images have no shell, so every operation is a subcommand).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/radioBros/FeatherDesk/internal/cli"
	"github.com/radioBros/FeatherDesk/internal/config"
)

// Set with -ldflags "-X main.version=… -X main.commit=…".
var (
	version = "dev"
	commit  = ""
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:]))
}

func run(ctx context.Context, args []string) int {
	name := "serve"
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			cli.Usage(os.Stdout)
			return 0
		}
		if args[0] != "" && args[0][0] != '-' {
			name, args = args[0], args[1:]
		}
	}
	c, ok := cli.Lookup(name)
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", name)
		cli.Usage(os.Stderr)
		return 2
	}
	if err := c.Run(ctx, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		var le *config.LoadError
		if errors.As(err, &le) {
			return 2 // configuration error
		}
		return 1
	}
	return 0
}
