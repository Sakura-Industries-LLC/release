package repogen

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/meigma/release/internal/execx"
	"github.com/meigma/release/internal/stage/pkgrepo"
)

const (
	// defaultDockerPath is resolved from PATH when [Options.DockerPath] is empty.
	defaultDockerPath = "docker"
	// defaultDebianImage contains apt-ftparchive from the accepted spike.
	defaultDebianImage = "debian@sha256:3a39a0592364683e6bab97937b72cad5a8fa6dcbbee90edb3bb48c7f8e94f258"
	// defaultFedoraImage contains createrepo_c from the accepted spike.
	defaultFedoraImage = "fedora@sha256:99e203b80b1c3d8f7e161ec10a68fd02b081ef83a3963553e513c82846b97814"
	// defaultAlpineImage contains apk and abuild-sign from the accepted spike.
	defaultAlpineImage = "alpine@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce"
	// containerRoot is the writable repository-tree mount.
	containerRoot = "/repo"
	// containerKeys is the read-only private-key directory.
	containerKeys = "/keys"
	// fileMode is the mode used for by-hash copies.
	fileMode = 0o644
	// directoryMode is the mode used for by-hash directories.
	directoryMode = 0o755
	// strongReleaseHashCount is two indexes, two architectures, and two algorithms.
	strongReleaseHashCount = 8
	// releaseHashFieldCount is digest, size, and path.
	releaseHashFieldCount = 3
	// hexCharactersPerByte is the encoded hexadecimal width of one byte.
	hexCharactersPerByte = 2
	// sha256Algorithm is the APT Release SHA-256 section name.
	sha256Algorithm = "SHA256"
	// sha512Algorithm is the APT Release SHA-512 section name.
	sha512Algorithm = "SHA512"
	// aptArchitectureAMD64 is the APT 64-bit x86 architecture.
	aptArchitectureAMD64 = "amd64"
	// aptArchitectureARM64 is the APT 64-bit Arm architecture.
	aptArchitectureARM64 = "arm64"
)

// Options configures a [Generator].
type Options struct {
	// DockerPath overrides the docker executable. Empty resolves "docker" from PATH.
	DockerPath string
	// DebianImage overrides the Debian repository-tool image.
	DebianImage string
	// FedoraImage overrides the Fedora repository-tool image.
	FedoraImage string
	// AlpineImage overrides the Alpine repository-tool image.
	AlpineImage string
	// APKSigningKey is the absolute aggregate APK index private-key path.
	APKSigningKey string
	// Environ is the child process environment. Nil inherits the parent.
	Environ []string
	// Stderr receives live container diagnostics. Nil discards them.
	Stderr io.Writer
}

// Generator regenerates all native repository metadata from canonical package trees.
type Generator struct {
	// dockerPath is the docker executable override.
	dockerPath string
	// debianImage is the configured APT tool image.
	debianImage string
	// fedoraImage is the configured RPM tool image.
	fedoraImage string
	// alpineImage is the configured APK tool image.
	alpineImage string
	// apkSigningKey is the aggregate APK index private key.
	apkSigningKey string
	// environ is the child process environment.
	environ []string
	// stderr receives live container diagnostics.
	stderr io.Writer
}

// releaseHash is one strong digest advertised by an APT Release file.
type releaseHash struct {
	// algorithm is SHA256 or SHA512.
	algorithm string
	// digest is the lowercase hexadecimal content digest.
	digest string
	// size is the advertised file size.
	size int64
	// name is the dists/<channel>-relative metadata path.
	name string
}

// New constructs a [Generator] with stable image defaults.
func New(options Options) *Generator {
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

	return &Generator{
		dockerPath:    options.DockerPath,
		debianImage:   debianImage,
		fedoraImage:   fedoraImage,
		alpineImage:   alpineImage,
		apkSigningKey: options.APKSigningKey,
		environ:       options.Environ,
		stderr:        options.Stderr,
	}
}

// Generate implements [pkgrepo.Generator].
func (g *Generator) Generate(ctx context.Context, request pkgrepo.GenerateRequest) error {
	if err := validateGenerateRequest(ctx, g, request); err != nil {
		return err
	}
	if err := g.generateAPT(ctx, request); err != nil {
		return err
	}
	root, err := os.OpenRoot(request.Root)
	if err != nil {
		return fmt.Errorf("open repository root: %w", err)
	}
	byHashErr := createAPTByHash(root, request.Channel)
	closeErr := root.Close()
	if byHashErr != nil {
		return byHashErr
	}
	if closeErr != nil {
		return fmt.Errorf("close repository root: %w", closeErr)
	}
	if err := g.generateRPM(ctx, request); err != nil {
		return err
	}
	if err := g.generateAPK(ctx, request); err != nil {
		return err
	}

	return nil
}

// validateGenerateRequest checks adapter configuration and deterministic inputs.
func validateGenerateRequest(ctx context.Context, generator *Generator, request pkgrepo.GenerateRequest) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	if generator == nil {
		return errors.New("repository generator is nil")
	}
	if !filepath.IsAbs(request.Root) {
		return fmt.Errorf("repository root %q is not absolute", request.Root)
	}
	if request.Channel != pkgrepo.ChannelStable {
		return fmt.Errorf("channel %q is unsupported", request.Channel)
	}
	if request.ReleaseTime.IsZero() {
		return errors.New("release time is zero")
	}
	if !request.ValidUntil.After(request.ReleaseTime) {
		return errors.New("valid-until time must follow release time")
	}
	if !filepath.IsAbs(generator.apkSigningKey) {
		return fmt.Errorf("APK signing key %q is not absolute", generator.apkSigningKey)
	}
	if err := validateOwnerOnlyFile(generator.apkSigningKey); err != nil {
		return err
	}
	keyName := filepath.Base(generator.apkSigningKey)
	if keyName == "." || keyName == string(filepath.Separator) || strings.ContainsAny(keyName, ":,\n\r") {
		return fmt.Errorf("APK signing key basename %q is unsafe", keyName)
	}

	return nil
}

// generateAPT runs apt-ftparchive and gzip with reproducible release metadata.
func (g *Generator) generateAPT(ctx context.Context, request pkgrepo.GenerateRequest) error {
	script := `set -eu
apt-get update -qq
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq apt-utils gzip >/dev/null
cd /repo/apt
for arch in amd64 arm64; do
  dir="dists/stable/main/binary-${arch}"
  mkdir -p "$dir"
  apt-ftparchive -a "$arch" packages pool/main >"$dir/Packages"
  gzip -n -9 -c "$dir/Packages" >"$dir/Packages.gz"
done
apt-ftparchive \
  -o APT::FTPArchive::Release::Origin=Meigma \
  -o APT::FTPArchive::Release::Label=Meigma \
  -o APT::FTPArchive::Release::Suite=stable \
  -o APT::FTPArchive::Release::Codename=stable \
  -o APT::FTPArchive::Release::Architectures="amd64 arm64" \
  -o APT::FTPArchive::Release::Components=main \
  -o APT::FTPArchive::Release::Acquire-By-Hash=yes \
  -o APT::FTPArchive::Release::Date="` + request.ReleaseTime.UTC().Format(http.TimeFormat) + `" \
  -o APT::FTPArchive::Release::Valid-Until="` + request.ValidUntil.UTC().Format(http.TimeFormat) + `" \
  release dists/stable >dists/stable/Release
`
	if err := g.runDocker(ctx, request.Root, nil, g.debianImage, "sh", "-ceu", script); err != nil {
		return fmt.Errorf("generate APT metadata: %w", err)
	}

	return nil
}

// generateRPM runs createrepo_c with unique metadata names and fixed timestamps.
func (g *Generator) generateRPM(ctx context.Context, request pkgrepo.GenerateRequest) error {
	epoch := strconv.FormatInt(request.ReleaseTime.Unix(), 10)
	script := `set -eu
dnf install -y -q createrepo_c >/dev/null
for arch in x86_64 aarch64; do
  touch -d "@` + epoch + `" "/repo/rpm/stable/${arch}/packages/"*.rpm
  createrepo_c --quiet --unique-md-filenames --revision ` + epoch + ` --set-timestamp-to-revision "/repo/rpm/stable/${arch}"
done
`
	if err := g.runDocker(ctx, request.Root, nil, g.fedoraImage, "sh", "-ceu", script); err != nil {
		return fmt.Errorf("generate RPM metadata: %w", err)
	}

	return nil
}

// generateAPK runs apk index and abuild-sign with a fixed source epoch.
func (g *Generator) generateAPK(ctx context.Context, request pkgrepo.GenerateRequest) error {
	epoch := strconv.FormatInt(request.ReleaseTime.Unix(), 10)
	keyName := filepath.Base(g.apkSigningKey)
	containerKey := path.Join(containerKeys, keyName)
	script := `set -eu
export SOURCE_DATE_EPOCH=` + epoch + `
apk add --no-cache abuild >/dev/null
cp /repo/keys/*.rsa.pub /etc/apk/keys/
for arch in x86_64 aarch64; do
  cd "/repo/apk/stable/main/${arch}"
  apk index -o APKINDEX.tar.gz ./*.apk
  abuild-sign -k "` + containerKey + `" APKINDEX.tar.gz
done
`
	keyMount := g.apkSigningKey + ":" + containerKey + ":ro"
	if err := g.runDocker(
		ctx,
		request.Root,
		[]string{"-v", keyMount},
		g.alpineImage,
		"sh",
		"-ceu",
		script,
	); err != nil {
		return fmt.Errorf("generate APK metadata: %w", err)
	}

	return nil
}

// runDocker runs one fixed repository-tool container against the writable tree.
func (g *Generator) runDocker(
	ctx context.Context,
	root string,
	mounts []string,
	image string,
	command ...string,
) error {
	args := []string{"run", "--rm", "-v", root + ":" + containerRoot}
	args = append(args, mounts...)
	args = append(args, image)
	args = append(args, command...)
	err := execx.Run(ctx, execx.Command{
		Program: defaultDockerPath,
		Path:    g.dockerPath,
		Args:    args,
		Env:     g.environ,
		Stderr:  g.stderr,
	})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return commandError(err)
	}

	return nil
}

// createAPTByHash copies every advertised SHA256 and SHA512 package index by digest.
func createAPTByHash(root *os.Root, channel pkgrepo.Channel) error {
	releaseName := path.Join("apt", "dists", string(channel), "Release")
	file, err := root.Open(releaseName)
	if err != nil {
		return fmt.Errorf("open APT Release: %w", err)
	}
	hashes, parseErr := parseReleaseHashes(file)
	closeErr := file.Close()
	if parseErr != nil {
		return parseErr
	}
	if closeErr != nil {
		return fmt.Errorf("close APT Release: %w", closeErr)
	}
	if len(hashes) != strongReleaseHashCount {
		return fmt.Errorf("APT Release advertises %d strong package-index hashes, want 8", len(hashes))
	}

	base := path.Join("apt", "dists", string(channel))
	for _, entry := range hashes {
		source := path.Join(base, entry.name)
		destination := path.Join(base, path.Dir(entry.name), "by-hash", entry.algorithm, entry.digest)
		if err := root.MkdirAll(path.Dir(destination), directoryMode); err != nil {
			return fmt.Errorf("create APT by-hash directory: %w", err)
		}
		if err := copyReleaseHash(root, source, destination, entry); err != nil {
			return err
		}
	}

	return nil
}

// parseReleaseHashes extracts required package-index entries from strong hash sections.
func parseReleaseHashes(reader io.Reader) ([]releaseHash, error) {
	hashes := make([]releaseHash, 0, strongReleaseHashCount)
	seen := make(map[string]struct{}, strongReleaseHashCount)
	algorithm := ""
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, " ") {
			algorithm = releaseHashAlgorithm(line)
			continue
		}
		entry, include, err := parseReleaseHashLine(algorithm, line, seen)
		if err != nil {
			return nil, err
		}
		if include {
			hashes = append(hashes, entry)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan APT Release: %w", err)
	}

	return hashes, nil
}

// releaseHashAlgorithm returns one supported strong-hash section name.
func releaseHashAlgorithm(line string) string {
	algorithm := strings.TrimSuffix(line, ":")
	if algorithm == sha256Algorithm || algorithm == sha512Algorithm {
		return algorithm
	}

	return ""
}

// parseReleaseHashLine validates and reserves one wanted package-index hash.
func parseReleaseHashLine(
	algorithm string,
	line string,
	seen map[string]struct{},
) (releaseHash, bool, error) {
	if algorithm == "" {
		return releaseHash{}, false, nil
	}
	fields := strings.Fields(line)
	if len(fields) != releaseHashFieldCount {
		return releaseHash{}, false, fmt.Errorf("malformed %s Release hash line %q", algorithm, line)
	}
	if !isPackageIndexPath(fields[2]) {
		return releaseHash{}, false, nil
	}
	size, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || size < 0 {
		return releaseHash{}, false, fmt.Errorf("invalid %s Release size %q", algorithm, fields[1])
	}
	expectedHex := sha256.Size * hexCharactersPerByte
	if algorithm == sha512Algorithm {
		expectedHex = sha512.Size * hexCharactersPerByte
	}
	if len(fields[0]) != expectedHex {
		return releaseHash{}, false, fmt.Errorf("invalid %s Release digest length %d", algorithm, len(fields[0]))
	}
	if _, err := hex.DecodeString(fields[0]); err != nil {
		return releaseHash{}, false, fmt.Errorf("invalid %s Release digest: %w", algorithm, err)
	}
	key := algorithm + ":" + fields[2]
	if _, duplicate := seen[key]; duplicate {
		return releaseHash{}, false, fmt.Errorf("duplicate %s Release path %q", algorithm, fields[2])
	}
	seen[key] = struct{}{}

	return releaseHash{
		algorithm: algorithm,
		digest:    strings.ToLower(fields[0]),
		size:      size,
		name:      fields[2],
	}, true, nil
}

// isPackageIndexPath accepts only the two canonical index names for both architectures.
func isPackageIndexPath(name string) bool {
	for _, architecture := range []string{aptArchitectureAMD64, aptArchitectureARM64} {
		base := path.Join("main", "binary-"+architecture)
		if name == path.Join(base, "Packages") || name == path.Join(base, "Packages.gz") {
			return true
		}
	}

	return false
}

// copyReleaseHash verifies the advertised size and digest while writing one by-hash object.
func copyReleaseHash(root *os.Root, sourceName, destinationName string, entry releaseHash) error {
	source, err := root.Open(sourceName)
	if err != nil {
		return fmt.Errorf("open APT index %s: %w", sourceName, err)
	}
	defer source.Close()
	destination, err := root.OpenFile(destinationName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		return fmt.Errorf("create APT by-hash object %s: %w", destinationName, err)
	}

	hasher := sha256.New()
	if entry.algorithm == sha512Algorithm {
		hasher = sha512.New()
	}
	written, copyErr := io.Copy(io.MultiWriter(destination, hasher), source)
	chmodErr := destination.Chmod(fileMode)
	closeErr := destination.Close()
	if copyErr != nil {
		return fmt.Errorf("copy APT by-hash object %s: %w", destinationName, copyErr)
	}
	if chmodErr != nil {
		return fmt.Errorf("chmod APT by-hash object %s: %w", destinationName, chmodErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close APT by-hash object %s: %w", destinationName, closeErr)
	}
	if written != entry.size {
		return fmt.Errorf("APT index %s has size %d, Release advertises %d", sourceName, written, entry.size)
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if actual != entry.digest {
		return fmt.Errorf(
			"APT index %s has %s digest %s, Release advertises %s",
			sourceName,
			entry.algorithm,
			actual,
			entry.digest,
		)
	}

	return nil
}

// validateOwnerOnlyFile requires an existing regular APK key without group or other access.
func validateOwnerOnlyFile(name string) error {
	info, err := os.Stat(name)
	if err != nil {
		return fmt.Errorf("stat APK signing key: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("APK signing key is not a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("APK signing key permissions %04o allow group or other access", info.Mode().Perm())
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
