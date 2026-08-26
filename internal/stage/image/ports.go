package image

import "context"

// APKBuilder compiles Melange packages and builds signed APK repositories.
//
// [APKBuilder.Build] first compiles the configuration for Sources[0].Arch,
// generates the ephemeral signing key at KeyPath, then builds one repository
// per source in order. Implementations invoke `melange` and must not require
// the caller to buffer a binary.
type APKBuilder interface {
	// Build compiles, signs, and writes APK repositories for request.
	//
	// request.Sources are built in order. The returned [APKRepositories.Dir]
	// must equal request.OutDir and [APKRepositories.PublicKey] must equal
	// request.KeyPath with a ".pub" suffix.
	Build(ctx context.Context, request APKBuildRequest) (APKRepositories, error)
}

// Composer locks an apko configuration and writes a multi-architecture OCI layout.
//
// [Composer.Build] first writes the lockfile, then builds the layout.
// Implementations invoke `apko` with cmd.Dir set to request.Dir.
type Composer interface {
	// Build locks request.Config and writes the OCI layout under request.Layout.
	//
	// Both invocations run with working directory request.Dir. Architecture
	// flags follow request.Arches in order.
	Build(ctx context.Context, request ComposeRequest) error
}

// APKBuildSource is one architecture's staged binary tree.
type APKBuildSource struct {
	// Arch is the APK architecture built from this source tree.
	Arch APKArch
	// Dir is the absolute directory containing the staged binary files.
	Dir string
}

// APKBuildRequest is the input to [APKBuilder.Build].
type APKBuildRequest struct {
	// Config is the absolute Melange configuration path.
	Config string
	// VarsFile is the absolute Melange vars file path.
	VarsFile string
	// KeyPath is the absolute ephemeral signing key path the builder generates.
	KeyPath string
	// OutDir is the absolute APK repository output directory.
	OutDir string
	// Sources is the build order; Sources[0].Arch is also the compile-check architecture.
	Sources []APKBuildSource
	// Runner is the container runner, "docker".
	Runner string
	// Namespace is the APK namespace.
	Namespace string
	// BuildDate is the RFC 3339 reproducible build timestamp.
	BuildDate string
	// GitRepoURL is the provenance repository URL.
	GitRepoURL string
	// GitCommit is the provenance commit SHA.
	GitCommit string
}

// APKRepositories is the output of [APKBuilder.Build].
type APKRepositories struct {
	// Dir is the repository root the build wrote.
	Dir string
	// PublicKey is the generated signing public key path.
	PublicKey string
}

// Annotation is one ordered OCI image annotation.
type Annotation struct {
	// Key is the annotation name.
	Key string
	// Value is the annotation value.
	Value string
}

// ComposeRequest is the input to [Composer.Build].
type ComposeRequest struct {
	// Dir is the absolute working directory both apko invocations run in.
	Dir string
	// Config is the Dir-relative apko configuration path.
	Config string
	// Repository is the Dir-relative APK repository path.
	Repository string
	// Keyring is the Dir-relative APK signing public key path.
	Keyring string
	// Lockfile is the Dir-relative lock output path.
	Lockfile string
	// SBOMPath is the Dir-relative SBOM output directory.
	SBOMPath string
	// Layout is the Dir-relative OCI layout output directory.
	Layout string
	// Reference is the local image reference, e.g. "local/release:1.2.3".
	Reference string
	// BuildDate is the RFC 3339 reproducible build timestamp.
	BuildDate string
	// Arches is the architecture list, one --arch flag per entry.
	Arches []APKArch
	// Annotations is the ordered annotation list applied to the image.
	Annotations []Annotation
}
