// SPDX-License-Identifier: AGPL-3.0-only

// Package cli is the subcommand registry of the simpletickets binary
// (PLT-011). Core commands live in cmd/simpletickets; modules add their own
// (create-admin, reset-password, reindex, …) from an init function:
//
//	func init() { cli.Register(cli.Command{Name: "create-admin", …}) }
package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"sync"
)

// Command is one subcommand. Run receives the arguments after the name.
type Command struct {
	Name    string
	Summary string
	Usage   string
	Run     func(ctx context.Context, args []string) error
}

var (
	mu   sync.Mutex
	cmds = map[string]Command{}
)

// Register adds a command; a duplicate name panics (programming error).
func Register(c Command) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := cmds[c.Name]; dup {
		panic("cli: duplicate command " + c.Name)
	}
	cmds[c.Name] = c
}

// Lookup finds a command by name.
func Lookup(name string) (Command, bool) {
	mu.Lock()
	defer mu.Unlock()
	c, ok := cmds[name]
	return c, ok
}

// All returns the commands sorted by name.
func All() []Command {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Command, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Usage prints the command list.
func Usage(w io.Writer) {
	fmt.Fprintln(w, "Usage: simpletickets <command> [flags]\n\nCommands:")
	for _, c := range All() {
		fmt.Fprintf(w, "  %-16s %s\n", c.Name, c.Summary)
	}
	fmt.Fprintln(w, "\nRun `simpletickets <command> -h` for a command's flags. Without a command, `serve` runs.")
}
