package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/wim-web/renstiq/internal/renstiq"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if len(os.Args) > 1 && os.Args[1] == "update" {
		os.Exit(renstiq.RunUpdate(ctx, os.Args[2:], os.Stdout, os.Stderr))
	}
	os.Exit(renstiq.RunCLI(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
