package pkgverify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/meigma/release/internal/execx"
	"github.com/meigma/release/internal/stage/pkgrepo"
)

const (
	// defaultDockerPath is resolved from PATH when [Options.DockerPath] is empty.
	defaultDockerPath = "docker"
	// runArgument starts one verification container.
	runArgument = "run"
	// removeArgument deletes the verification container after it exits.
	removeArgument = "--rm"
	// networkNoneArgument disables container networking during package verification.
	networkNoneArgument = "--network=none"
	// readOnlyArgument makes the verification container root filesystem immutable.
	readOnlyArgument = "--read-only"
	// temporaryFilesystemArgument mounts an in-memory writable scratch directory.
	temporaryFilesystemArgument = "--tmpfs"
	// temporaryDirectory is the container scratch mount point.
	temporaryDirectory = "/tmp"
	// defaultFedoraImage contains the rpmkeys version used by the accepted spike.
	defaultFedoraImage = "fedora@sha256:99e203b80b1c3d8f7e161ec10a68fd02b081ef83a3963553e513c82846b97814"
	// defaultAlpineImage contains the apk version used by the accepted spike.
	defaultAlpineImage = "alpine@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce"
	// containerRPMPath is the read-only RPM package mount path.
	containerRPMPath = "/package.rpm"
	// containerAPKPath is the read-only APK package mount path.
	containerAPKPath = "/package.apk"
	// containerRPMKeyPath is the read-only RPM public-key mount path.
	containerRPMKeyPath = "/key.asc"
	// containerRPMDBPath is the temporary RPM database mount path.
	containerRPMDBPath = "/rpmdb"
	// containerAPKKeysPath is the APK public-key directory.
	containerAPKKeysPath = "/keys"
)

// Options configures a [Verifier].
type Options struct {
	// DockerPath overrides the docker executable. Empty resolves "docker" from PATH.
	DockerPath string
	// FedoraImage overrides the pinned RPM verification image.
	FedoraImage string
	// AlpineImage overrides the pinned APK verification image.
	AlpineImage string
	// Environ is the child process environment. Nil inherits the parent.
	Environ []string
	// Stderr receives live container diagnostics. Nil discards them.
	Stderr io.Writer
}

// Verifier checks producer-native package signatures in isolated containers.
type Verifier struct {
	// dockerPath is the docker executable override.
	dockerPath string
	// fedoraImage is the configured RPM verification image.
	fedoraImage string
	// alpineImage is the configured APK verification image.
	alpineImage string
	// environ is the child process environment.
	environ []string
	// stderr receives live container diagnostics.
	stderr io.Writer
}

// New constructs a [Verifier] with stable image defaults.
func New(options Options) *Verifier {
	fedoraImage := options.FedoraImage
	if fedoraImage == "" {
		fedoraImage = defaultFedoraImage
	}
	alpineImage := options.AlpineImage
	if alpineImage == "" {
		alpineImage = defaultAlpineImage
	}

	return &Verifier{
		dockerPath:  options.DockerPath,
		fedoraImage: fedoraImage,
		alpineImage: alpineImage,
		environ:     options.Environ,
		stderr:      options.Stderr,
	}
}

// Verify implements [pkgrepo.Verifier].
func (v *Verifier) Verify(ctx context.Context, request pkgrepo.VerificationRequest) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	if v == nil {
		return errors.New("package verifier is nil")
	}
	if !filepath.IsAbs(request.Package) {
		return fmt.Errorf("package path %q is not absolute", request.Package)
	}
	if !filepath.IsAbs(request.PublicKey) {
		return fmt.Errorf("public key path %q is not absolute", request.PublicKey)
	}

	switch request.Format {
	case pkgrepo.FormatDEB:
		return fmt.Errorf("package format %q does not have a native verifier", request.Format)
	case pkgrepo.FormatRPM:
		return v.verifyRPM(ctx, request)
	case pkgrepo.FormatAPK:
		return v.verifyAPK(ctx, request)
	default:
		return fmt.Errorf("package format %q does not have a native verifier", request.Format)
	}
}

// verifyRPM imports one public key into an ephemeral database and checks the package.
func (v *Verifier) verifyRPM(ctx context.Context, request pkgrepo.VerificationRequest) error {
	database, err := os.MkdirTemp("", "release-rpmdb-")
	if err != nil {
		return fmt.Errorf("create temporary RPM database: %w", err)
	}
	defer os.RemoveAll(database)

	if err := v.runDocker(ctx, []string{
		"-v", request.PublicKey + ":" + containerRPMKeyPath + ":ro",
		"-v", database + ":" + containerRPMDBPath,
	}, v.fedoraImage, "rpmkeys", "--dbpath", containerRPMDBPath, "--import", containerRPMKeyPath); err != nil {
		return fmt.Errorf("import RPM public key: %w", err)
	}
	if err := v.runDocker(ctx, []string{
		"-v", request.Package + ":" + containerRPMPath + ":ro",
		"-v", database + ":" + containerRPMDBPath,
	}, v.fedoraImage, "rpmkeys", "--dbpath", containerRPMDBPath, "--checksig", containerRPMPath); err != nil {
		return fmt.Errorf("verify RPM signature: %w", err)
	}

	return nil
}

// verifyAPK checks one package against a key mounted under its signature basename.
func (v *Verifier) verifyAPK(ctx context.Context, request pkgrepo.VerificationRequest) error {
	keyName := filepath.Base(request.PublicKey)
	containerKey := containerAPKKeysPath + "/" + keyName
	if err := v.runDocker(ctx, []string{
		"-v", request.Package + ":" + containerAPKPath + ":ro",
		"-v", request.PublicKey + ":" + containerKey + ":ro",
	}, v.alpineImage, "apk", "verify", "--keys-dir", containerAPKKeysPath, containerAPKPath); err != nil {
		return fmt.Errorf("verify APK signature: %w", err)
	}

	return nil
}

// runDocker runs one fixed networkless read-only verification command.
func (v *Verifier) runDocker(ctx context.Context, mounts []string, image string, command ...string) error {
	args := []string{
		runArgument,
		removeArgument,
		networkNoneArgument,
		readOnlyArgument,
		temporaryFilesystemArgument,
		temporaryDirectory,
	}
	args = append(args, mounts...)
	args = append(args, image)
	args = append(args, command...)
	err := execx.Run(ctx, execx.Command{
		Program: defaultDockerPath,
		Path:    v.dockerPath,
		Args:    args,
		Env:     v.environ,
		Stderr:  v.stderr,
	})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return commandError(err)
	}

	return nil
}

// commandError maps one process failure without exposing arguments or environment.
func commandError(err error) error {
	var runErr *execx.RunError
	if !errors.As(err, &runErr) {
		return err
	}
	if exitCode, exited := runErr.ExitCode(); exited {
		if tail := strings.TrimSpace(runErr.StderrTail()); tail != "" {
			return fmt.Errorf("docker exited with code %d: %s", exitCode, tail)
		}
		return fmt.Errorf("docker exited with code %d", exitCode)
	}

	return err
}
