package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/meigma/release/internal/cli"
)

//nolint:gochecknoglobals // Linker-injected build metadata.
var (
	version = "dev"
	commit  = "none"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	root := cli.NewRootCommand(cli.Options{
		In:  os.Stdin,
		Out: os.Stdout,
		Err: os.Stderr,
		Build: cli.BuildInfo{
			Version: version,
			Commit:  commit,
		},
	})
	if err := root.ExecuteContext(ctx); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 1
	}

	return 0
}
