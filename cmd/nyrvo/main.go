// Command nyrvo captures and compares execution environments.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/nyrvo-dev/nyrvo/internal/cli"
)

func main() {
	// Collectors spawn external tools; cancelling on interrupt lets them stop
	// promptly instead of leaving the terminal waiting on a probe.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	os.Exit(cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
