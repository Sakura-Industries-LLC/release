package pkgrepo

import (
	"context"
	"io"
	"os"
)

// ReleaseSource downloads one exact public GitHub Release into a confined root.
type ReleaseSource interface {
	// Fetch downloads every asset and returns the release's verified transport facts.
	Fetch(ctx context.Context, request ReleaseRequest, destination *os.Root) (Release, error)
}

// Attestor verifies GitHub build provenance for one downloaded package.
type Attestor interface {
	// Verify proves the artifact, source ref, source digest, and signer workflow binding.
	Verify(ctx context.Context, request AttestationRequest) error
}

// Store reads and writes one static package-repository object namespace.
type Store interface {
	// List returns every object in the namespace across all service pages.
	List(ctx context.Context) ([]StoredObject, error)
	// Download streams one object and returns its stored digest metadata and size.
	Download(ctx context.Context, name string, destination io.Writer) (StoredContent, error)
	// Stat returns one object's stored digest metadata and size.
	//
	// The boolean is false only when the object does not exist.
	Stat(ctx context.Context, name string) (StoredContent, bool, error)
	// Upload writes one exact generated object and its cache and digest metadata.
	Upload(ctx context.Context, request UploadRequest) error
}

// Installer verifies requested packages through native package managers.
type Installer interface {
	// Verify installs every requested package through APT, DNF, and APK.
	Verify(ctx context.Context, request InstallRequest) error
}
