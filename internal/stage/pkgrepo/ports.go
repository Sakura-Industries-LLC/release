package pkgrepo

import "context"

// Inspector reads normalized metadata from one native package file.
type Inspector interface {
	// Inspect reads package without trusting its filename.
	Inspect(ctx context.Context, format Format, packagePath string) (PackageMetadata, error)
}

// Verifier checks producer-native RPM and APK package signatures.
type Verifier interface {
	// Verify checks one package against its configured producer public key.
	Verify(ctx context.Context, request VerificationRequest) error
}

// Generator regenerates APT, RPM, and APK metadata from a complete package tree.
type Generator interface {
	// Generate writes deterministic metadata beneath request.Root.
	//
	// The generated APT Release file remains unsigned. RPM repomd.xml files
	// remain unsigned. APKINDEX.tar.gz files must already carry the configured
	// aggregate APK index signature when this method returns.
	Generate(ctx context.Context, request GenerateRequest) error
}

// Signer creates aggregate APT and RPM metadata signatures.
type Signer interface {
	// ClearSign writes an inline OpenPGP signature for request.Input.
	ClearSign(ctx context.Context, request SignRequest) error
	// DetachSign writes an armored detached OpenPGP signature for request.Input.
	DetachSign(ctx context.Context, request SignRequest) error
}
