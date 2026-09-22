// Command testclassify discovers Go and Kotlin tests, classifies each test's
// execution boundary with the TypeSafe AI Jev model, and prints a table report.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/mholtzscher/test-type-classifier/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
