package reg

import (
	"context"
	"fmt"
	"net/http"

	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"

	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/puboci"
)

// Credentials is a registry username and password.
//
// A zero value is an anonymous read. Password is a [rel.Secret] so token
// text is not printed, logged, or encoded.
type Credentials struct {
	// Username is the registry user. An empty username with a password is
	// valid for token auth.
	Username string

	// Password is the registry password or token. Reveal it only when
	// composing the oras credential.
	Password rel.Secret
}

// Options configures a read-only registry [Client].
type Options struct {
	// Credentials authenticates registry reads. The zero value is anonymous.
	Credentials Credentials

	// PlainHTTP forces HTTP instead of HTTPS. Tests use this against a
	// local registry.
	PlainHTTP bool

	// HTTPClient is the optional transport. Nil selects
	// [retry.DefaultClient]. An injected client is used as-is so tests stay
	// deterministic.
	HTTPClient *http.Client
}

// Client reads tag state from a GHCR-compatible registry.
//
// It implements [puboci.StateReader]. It never pushes, tags, or deletes.
// Credential material is captured inside the auth client's closure and is
// not stored on this value.
type Client struct {
	// auth is the shared oras auth client.
	auth *auth.Client

	// plainHTTP forces HTTP instead of HTTPS.
	plainHTTP bool

	// authenticated reports whether New captured credentials.
	authenticated bool
}

// New constructs a [Client] from options.
//
// A nil HTTPClient selects [retry.DefaultClient]. When credentials are
// present, [rel.Secret.Reveal] is called once and the token is captured
// only inside the auth credential closure.
func New(options Options) *Client {
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = retry.DefaultClient
	}

	authClient := &auth.Client{
		Client: httpClient,
		Cache:  auth.NewCache(),
	}
	authenticated := options.Credentials.Username != "" || !options.Credentials.Password.IsEmpty()
	if authenticated {
		cred := auth.Credential{
			Username: options.Credentials.Username,
			Password: options.Credentials.Password.Reveal(),
		}
		authClient.Credential = func(context.Context, string) (auth.Credential, error) {
			return cred, nil
		}
	}

	return &Client{
		auth:          authClient,
		plainHTTP:     options.PlainHTTP,
		authenticated: authenticated,
	}
}

// repository builds a remote repository client for ref.
func (c *Client) repository(ref puboci.Reference) (*remote.Repository, error) {
	repo, err := remote.NewRepository(ref.Image.String())
	if err != nil {
		return nil, fmt.Errorf("parse repository: %w", err)
	}

	repo.Client = c.auth
	repo.PlainHTTP = c.plainHTTP

	return repo, nil
}
