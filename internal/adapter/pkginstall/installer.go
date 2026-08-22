package pkginstall

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/meigma/release/internal/execx"
	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/pkgrepo"
)

const (
	// defaultDockerPath is resolved from PATH when [Options.DockerPath] is empty.
	defaultDockerPath = "docker"
	// defaultDebianImage pins the APT client used by repository generation.
	defaultDebianImage = "debian@sha256:3a39a0592364683e6bab97937b72cad5a8fa6dcbbee90edb3bb48c7f8e94f258"
	// defaultFedoraImage pins the DNF client used by repository generation.
	defaultFedoraImage = "fedora@sha256:99e203b80b1c3d8f7e161ec10a68fd02b081ef83a3963553e513c82846b97814"
	// defaultAlpineImage pins the APK client used by repository generation.
	defaultAlpineImage = "alpine@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce"
	// localRepositoryRoot is the read-only generated repository mount point.
	localRepositoryRoot = "/repo"
	// publicKeyRoot is the read-only public-key mount point.
	publicKeyRoot = "/keys"
	// expectedVersionVariable carries the exact expected package version.
	expectedVersionVariable = "EXPECTED_VERSION"
	// repositoryRootVariable carries the local or public repository root.
	repositoryRootVariable = "REPOSITORY_ROOT"
	// aptKeyVariable carries the container path to the aggregate APT key.
	aptKeyVariable = "APT_KEY"
	// rpmKeyURLsVariable carries whitespace-separated metadata and package key URLs.
	rpmKeyURLsVariable = "RPM_KEY_URLS"
	// apkKeysVariable carries colon-separated index and package key paths.
	apkKeysVariable = "APK_KEYS"
)

const (
	// aptInstallScript configures one signed source and verifies exact installed versions.
	aptInstallScript = `set -eu
rm -f /etc/apt/sources.list.d/*
printf 'deb [signed-by=%s] %s/apt stable main\n' "$APT_KEY" "$REPOSITORY_ROOT" >/etc/apt/sources.list
apt-get update -qq
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "$@" >/dev/null
for package do
  actual=$(dpkg-query -W -f='${Version}' "$package")
  test "$actual" = "$EXPECTED_VERSION"
done
`
	// dnfInstallScript configures signature checks and verifies exact installed versions.
	dnfInstallScript = `set -eu
cat >/etc/yum.repos.d/release.repo <<EOF
[release]
name=Release
baseurl=${REPOSITORY_ROOT}/rpm/stable/\$basearch
enabled=1
gpgcheck=1
repo_gpgcheck=1
gpgkey=${RPM_KEY_URLS}
EOF
dnf --disablerepo='*' --enablerepo=release install -y -q "$@" >/dev/null
for package do
  actual=$(rpm -q --qf '%{VERSION}' "$package")
  test "$actual" = "$EXPECTED_VERSION"
done
`
	// apkInstallScript configures the signed index and verifies exact installed versions.
	apkInstallScript = `set -eu
old_ifs=$IFS
IFS=:
for key in $APK_KEYS; do
  cp "$key" /etc/apk/keys/
done
IFS=$old_ifs
printf '%s/apk/stable/main\n' "$REPOSITORY_ROOT" >/etc/apk/repositories
apk update --no-progress >/dev/null
apk add --no-progress "$@" >/dev/null
for package do
  actual=$(apk info -v "$package")
  test "$actual" = "$package-$EXPECTED_VERSION-r0"
done
`
)

// Options configures an [Installer].
type Options struct {
	// DockerPath overrides the docker executable. Empty resolves "docker" from PATH.
	DockerPath string
	// DebianImage overrides the pinned APT client image.
	DebianImage string
	// FedoraImage overrides the pinned DNF client image.
	FedoraImage string
	// AlpineImage overrides the pinned APK client image.
	AlpineImage string
	// Environ is the docker process environment. Nil inherits the parent environment.
	Environ []string
	// Stdout receives live package-manager output. Nil discards it.
	Stdout io.Writer
	// Stderr receives live package-manager diagnostics. Nil discards them.
	Stderr io.Writer
}

// Installer invokes disposable native package-manager clients.
type Installer struct {
	// dockerPath is the configured docker executable.
	dockerPath string
	// debianImage is the APT client image.
	debianImage string
	// fedoraImage is the DNF client image.
	fedoraImage string
	// alpineImage is the APK client image.
	alpineImage string
	// environ is the docker process environment.
	environ []string
	// stdout receives live process output.
	stdout io.Writer
	// stderr receives live process diagnostics.
	stderr io.Writer
}

// New constructs a native package installer.
func New(options Options) *Installer {
	debianImage := options.DebianImage
	if debianImage == "" {
		debianImage = defaultDebianImage
	}
	fedoraImage := options.FedoraImage
	if fedoraImage == "" {
		fedoraImage = defaultFedoraImage
	}
	alpineImage := options.AlpineImage
	if alpineImage == "" {
		alpineImage = defaultAlpineImage
	}

	return &Installer{
		dockerPath:  options.DockerPath,
		debianImage: debianImage,
		fedoraImage: fedoraImage,
		alpineImage: alpineImage,
		environ:     options.Environ,
		stdout:      options.Stdout,
		stderr:      options.Stderr,
	}
}

// Verify implements [pkgrepo.Installer].
//
// It runs APT, DNF, and APK in disposable containers. Local repository checks
// mount the generated tree read-only with networking disabled; public checks
// use the configured HTTPS origin. Native signature verification remains
// enabled in every client, and each installed version must match exactly.
func (i *Installer) Verify(ctx context.Context, request pkgrepo.InstallRequest) error {
	validated, err := validateRequest(ctx, i, request)
	if err != nil {
		return err
	}
	clients := []clientRequest{
		{format: "APT", image: i.debianImage, script: aptInstallScript},
		{format: "DNF", image: i.fedoraImage, script: dnfInstallScript},
		{format: "APK", image: i.alpineImage, script: apkInstallScript},
	}
	for _, client := range clients {
		if err := i.run(ctx, request, validated, client); err != nil {
			return fmt.Errorf("verify %s installation: %w", client.format, err)
		}
	}

	return nil
}

// validatedRequest contains derived container-safe request values.
type validatedRequest struct {
	// repositoryRoot is the local file URL root or public HTTPS origin.
	repositoryRoot string
	// rootPath is the optional absolute host repository mount.
	rootPath string
	// keysPath is the absolute host public-key mount.
	keysPath string
	// aptKey is the aggregate APT key path inside the container.
	aptKey string
	// rpmKeyURLs contains aggregate and producer key file URLs for DNF.
	rpmKeyURLs string
	// apkKeys contains aggregate and producer key paths for APK.
	apkKeys string
}

// clientRequest describes one native package-manager container.
type clientRequest struct {
	// format names the client for returned errors.
	format string
	// image is the pinned client image.
	image string
	// script configures, installs, and verifies packages.
	script string
}

// validateRequest checks all installer boundaries before starting a container.
func validateRequest(
	ctx context.Context,
	installer *Installer,
	request pkgrepo.InstallRequest,
) (validatedRequest, error) {
	if ctx == nil {
		return validatedRequest{}, errors.New("context is nil")
	}
	if installer == nil {
		return validatedRequest{}, errors.New("installer is nil")
	}
	keysPath, err := validateKeys(request)
	if err != nil {
		return validatedRequest{}, err
	}
	if request.Channel != pkgrepo.ChannelStable {
		return validatedRequest{}, fmt.Errorf("installation channel %q is unsupported", request.Channel)
	}
	if _, parseErr := rel.ParseVersion(request.Version.String()); parseErr != nil {
		return validatedRequest{}, fmt.Errorf("installation version: %w", parseErr)
	}
	if len(request.Packages) == 0 {
		return validatedRequest{}, errors.New("installation package list is empty")
	}
	for _, name := range request.Packages {
		if _, parseErr := pkgrepo.ParsePackageName(string(name)); parseErr != nil {
			return validatedRequest{}, parseErr
		}
	}

	validated := validatedRequest{
		keysPath:   keysPath,
		aptKey:     containerKeyPath(request.APTKey),
		rpmKeyURLs: keyURLs(request.RPMKeys),
		apkKeys:    strings.Join(containerKeyPaths(request.APKKeys), ":"),
	}
	if request.Root != nil {
		validated.rootPath = request.Root.Name()
		if !filepath.IsAbs(validated.rootPath) {
			return validatedRequest{}, errors.New("local repository root is not absolute")
		}
		validated.repositoryRoot = "file://" + localRepositoryRoot
		return validated, nil
	}
	origin, err := url.Parse(request.Origin)
	if err != nil {
		return validatedRequest{}, fmt.Errorf("parse installation origin: %w", err)
	}
	if origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.RawQuery != "" ||
		origin.Fragment != "" {
		return validatedRequest{}, fmt.Errorf("installation origin %q is not a clean HTTPS origin", request.Origin)
	}
	validated.repositoryRoot = strings.TrimSuffix(request.Origin, "/")

	return validated, nil
}

// validateKeys checks the reviewed key root and every configured public-key path.
func validateKeys(request pkgrepo.InstallRequest) (string, error) {
	if request.Keys == nil {
		return "", errors.New("installation key root is nil")
	}
	keysPath := request.Keys.Name()
	if !filepath.IsAbs(keysPath) {
		return "", errors.New("installation key root is not absolute")
	}
	if len(request.RPMKeys) == 0 {
		return "", errors.New("RPM installation key list is empty")
	}
	if len(request.APKKeys) == 0 {
		return "", errors.New("APK installation key list is empty")
	}
	keys := []struct {
		label string
		path  string
	}{{label: "APT", path: request.APTKey}}
	for _, key := range request.RPMKeys {
		keys = append(keys, struct {
			label string
			path  string
		}{label: "RPM", path: key})
	}
	for _, key := range request.APKKeys {
		keys = append(keys, struct {
			label string
			path  string
		}{label: "APK", path: key})
	}
	for _, key := range keys {
		if !fs.ValidPath(key.path) || key.path == "." {
			return "", fmt.Errorf("%s installation key path %q is not confined", key.label, key.path)
		}
		info, err := fs.Stat(request.Keys.FS(), key.path)
		if err != nil {
			return "", fmt.Errorf("stat %s installation key: %w", key.label, err)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("%s installation key is not a regular file", key.label)
		}
	}

	return keysPath, nil
}

// containerKeyPath maps one reviewed key path onto its read-only container mount.
func containerKeyPath(key string) string {
	return filepath.ToSlash(filepath.Join(publicKeyRoot, key))
}

// containerKeyPaths maps reviewed key paths onto their read-only container mount.
func containerKeyPaths(keys []string) []string {
	paths := make([]string, 0, len(keys))
	for _, key := range keys {
		paths = append(paths, containerKeyPath(key))
	}

	return paths
}

// keyURLs formats reviewed key paths as DNF file URLs.
func keyURLs(keys []string) string {
	paths := containerKeyPaths(keys)
	for index := range paths {
		paths[index] = "file://" + paths[index]
	}

	return strings.Join(paths, " ")
}

// run executes one native package-manager client.
func (i *Installer) run(
	ctx context.Context,
	request pkgrepo.InstallRequest,
	validated validatedRequest,
	client clientRequest,
) error {
	arguments := []string{"run", "--rm"}
	if validated.rootPath != "" {
		arguments = append(
			arguments,
			"--network=none",
			"--mount",
			mountArgument(validated.rootPath, localRepositoryRoot),
		)
	}
	arguments = append(
		arguments,
		"--mount", mountArgument(validated.keysPath, publicKeyRoot),
		"-e", expectedVersionVariable+"="+request.Version.String(),
		"-e", repositoryRootVariable+"="+validated.repositoryRoot,
		"-e", aptKeyVariable+"="+validated.aptKey,
		"-e", rpmKeyURLsVariable+"="+validated.rpmKeyURLs,
		"-e", apkKeysVariable+"="+validated.apkKeys,
		client.image,
		"sh", "-c", client.script, "install",
	)
	for _, name := range request.Packages {
		arguments = append(arguments, string(name))
	}

	err := execx.Run(ctx, execx.Command{
		Program: defaultDockerPath,
		Path:    i.dockerPath,
		Args:    arguments,
		Env:     i.environ,
		Stdout:  i.stdout,
		Stderr:  i.stderr,
	})
	if err != nil {
		return commandError(err)
	}

	return nil
}

// mountArgument formats one read-only bind mount without shell parsing.
func mountArgument(source, destination string) string {
	return "type=bind,src=" + source + ",dst=" + destination + ",readonly"
}

// commandError maps one Docker process failure without exposing its environment.
func commandError(err error) error {
	var runErr *execx.RunError
	if !errors.As(err, &runErr) {
		return err
	}
	exitCode, exited := runErr.ExitCode()
	if !exited {
		return err
	}
	if tail := strings.TrimSpace(runErr.StderrTail()); tail != "" {
		return fmt.Errorf("docker exited with code %d: %s", exitCode, tail)
	}

	return fmt.Errorf("docker exited with code %d", exitCode)
}
