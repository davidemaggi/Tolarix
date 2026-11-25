// Command tolarix is the single Tolarix binary: HTTP server, scheduler and CLI.
package main

import (
	"context"
	"os"

	"github.com/davidemaggi/tolarix/core/internal/cli"
)

func main() {
	os.Exit(cli.Execute(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
