package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/meigma/release/internal/adapter/ghact"
	"github.com/meigma/release/internal/adapter/reg"
	"github.com/meigma/release/internal/cli"
	"github.com/meigma/release/internal/stage/pubgh"
	"github.com/meigma/release/internal/stage/puboci"
)

//nolint:gochecknoglobals // Linker-injected build metadata.
var (
	version = "dev"
	commit  = "none"
)

// main is the process entrypoint.
func main() {
	os.Exit(run())
}

// run constructs the command tree and returns the process exit code.
func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	root := cli.NewRootCommand(cli.Options{
		In:  os.Stdin,
		Out: os.Stdout,
		Err: os.Stderr,
		NewArtifactMeta: func(token string, endpoint cli.GitHubEndpoint) (pubgh.ArtifactMeta, error) {
			return ghact.NewAuthenticated(token, endpoint.APIURL, endpoint.ServerURL)
		},
		NewStateReader: func(credentials cli.RegistryCredentials) (puboci.StateReader, error) {
			return reg.New(reg.Options{
				Credentials: reg.Credentials{
					Username: credentials.Username,
					Password: credentials.Password,
				},
			}), nil
		},
		Build: cli.BuildInfo{
			Version:  version,
			Commit:   commit,
			Protocol: cli.Protocol,
		},
	})
	if err := root.ExecuteContext(ctx); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return cli.ExitCode(err)
	}

	return 0
}
