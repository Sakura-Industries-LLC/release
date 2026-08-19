package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/meigma/release/internal/adapter/cosign"
	"github.com/meigma/release/internal/adapter/ghact"
	"github.com/meigma/release/internal/adapter/ghrel"
	"github.com/meigma/release/internal/adapter/ghup"
	"github.com/meigma/release/internal/adapter/gitx"
	"github.com/meigma/release/internal/adapter/reg"
	"github.com/meigma/release/internal/cli"
	"github.com/meigma/release/internal/rel"
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
		NewStateReader: func(config cli.RegistryConfig) (puboci.StateReader, error) {
			return newRegistryClient(config), nil
		},
		NewContentPusher: func(config cli.RegistryConfig) (puboci.ContentPusher, error) {
			return newRegistryClient(config), nil
		},
		NewTagCommitter: func(config cli.RegistryConfig) (puboci.TagCommitter, error) {
			return newRegistryClient(config), nil
		},
		NewSigner: func(path string) (puboci.Signer, error) {
			return cosign.New(cosign.Options{
				Path:   path,
				Stderr: os.Stderr,
			}), nil
		},
		NewBlobVerifier: func(path, dir string) (pubgh.BlobVerifier, error) {
			return cosign.NewVerifier(cosign.VerifierOptions{
				Path:   path,
				Dir:    dir,
				Stderr: os.Stderr,
			}), nil
		},
		NewReleaseReader: func(token rel.Secret, endpoint cli.GitHubEndpoint) (pubgh.ReleaseReader, error) {
			return ghrel.NewAuthenticated(token, endpoint.APIURL, endpoint.ServerURL)
		},
		NewPublisher: func(token rel.Secret, endpoint cli.GitHubEndpoint) (pubgh.Publisher, error) {
			return ghrel.NewAuthenticated(token, endpoint.APIURL, endpoint.ServerURL)
		},
		NewAssetReplacer: func(token rel.Secret, path, dir string) (pubgh.AssetReplacer, error) {
			return ghup.New(ghup.Options{
				Path:   path,
				Dir:    dir,
				Token:  token,
				Stderr: os.Stderr,
			}), nil
		},
		NewRefResolver: func(path, dir string) (pubgh.RefResolver, error) {
			return gitx.New(gitx.Options{
				Path:   path,
				Dir:    dir,
				Stderr: os.Stderr,
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

// newRegistryClient constructs the shared registry adapter from resolved config.
func newRegistryClient(config cli.RegistryConfig) *reg.Client {
	return reg.New(reg.Options{
		Credentials: reg.Credentials{
			Username: config.Credentials.Username,
			Password: config.Credentials.Password,
		},
		PlainHTTP: config.PlainHTTP,
	})
}
