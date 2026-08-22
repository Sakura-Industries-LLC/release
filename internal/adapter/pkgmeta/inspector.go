package pkgmeta

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/meigma/release/internal/execx"
	"github.com/meigma/release/internal/rel"
	"github.com/meigma/release/internal/stage/pkgrepo"
)

const (
	// defaultDockerPath is resolved from PATH when [Options.DockerPath] is empty.
	defaultDockerPath = "docker"
	// runArgument starts one inspection container.
	runArgument = "run"
	// removeArgument deletes the inspection container after it exits.
	removeArgument = "--rm"
	// networkNoneArgument disables container networking during package inspection.
	networkNoneArgument = "--network=none"
	// readOnlyArgument makes the inspection container root filesystem immutable.
	readOnlyArgument = "--read-only"
	// temporaryFilesystemArgument mounts an in-memory writable scratch directory.
	temporaryFilesystemArgument = "--tmpfs"
	// temporaryDirectory is the container scratch mount point.
	temporaryDirectory = "/tmp"
	// defaultDebianImage contains the dpkg-deb version used by the accepted spike.
	defaultDebianImage = "debian@sha256:3a39a0592364683e6bab97937b72cad5a8fa6dcbbee90edb3bb48c7f8e94f258"
	// defaultFedoraImage contains the rpm version used by the accepted spike.
	defaultFedoraImage = "fedora@sha256:99e203b80b1c3d8f7e161ec10a68fd02b081ef83a3963553e513c82846b97814"
	// containerPackagePath is the read-only package mount path.
	containerPackagePath = "/package"
	// rpmMetadataFieldCount is the NAME, VERSION, RELEASE, and ARCH field count.
	rpmMetadataFieldCount = 4
	// maxAPKStreamSize bounds one gzip stream's decompressed bytes.
	maxAPKStreamSize int64 = 512 * 1024 * 1024
	// packageInfoFile is the APK control metadata member.
	packageInfoFile = ".PKGINFO"
)

// Options configures an [Inspector].
type Options struct {
	// DockerPath overrides the docker executable. Empty resolves "docker" from PATH.
	DockerPath string
	// DebianImage overrides the pinned Debian inspection image.
	DebianImage string
	// FedoraImage overrides the pinned Fedora inspection image.
	FedoraImage string
	// Environ is the child process environment. Nil inherits the parent.
	Environ []string
	// Stderr receives live container diagnostics. Nil discards them.
	Stderr io.Writer
}

// Inspector reads native package metadata without trusting filenames.
//
// DEB and RPM inspection runs fixed commands in networkless read-only
// containers. APK inspection parses the concatenated gzip/tar package streams
// directly and reads only the .PKGINFO control member.
type Inspector struct {
	// dockerPath is the docker executable override.
	dockerPath string
	// debianImage is the configured DEB inspection image.
	debianImage string
	// fedoraImage is the configured RPM inspection image.
	fedoraImage string
	// environ is the child process environment.
	environ []string
	// stderr receives live container diagnostics.
	stderr io.Writer
}

// New constructs an [Inspector] with stable image defaults.
func New(options Options) *Inspector {
	debianImage := options.DebianImage
	if debianImage == "" {
		debianImage = defaultDebianImage
	}
	fedoraImage := options.FedoraImage
	if fedoraImage == "" {
		fedoraImage = defaultFedoraImage
	}

	return &Inspector{
		dockerPath:  options.DockerPath,
		debianImage: debianImage,
		fedoraImage: fedoraImage,
		environ:     options.Environ,
		stderr:      options.Stderr,
	}
}

// Inspect implements [pkgrepo.Inspector].
func (i *Inspector) Inspect(
	ctx context.Context,
	format pkgrepo.Format,
	packagePath string,
) (pkgrepo.PackageMetadata, error) {
	if ctx == nil {
		return pkgrepo.PackageMetadata{}, errors.New("context is nil")
	}
	if i == nil {
		return pkgrepo.PackageMetadata{}, errors.New("package metadata inspector is nil")
	}
	if packagePath == "" {
		return pkgrepo.PackageMetadata{}, errors.New("package path is empty")
	}

	switch format {
	case pkgrepo.FormatDEB:
		return i.inspectDEB(ctx, packagePath)
	case pkgrepo.FormatRPM:
		return i.inspectRPM(ctx, packagePath)
	case pkgrepo.FormatAPK:
		return inspectAPK(packagePath)
	default:
		return pkgrepo.PackageMetadata{}, fmt.Errorf("package format %q is unsupported", format)
	}
}

// inspectDEB queries Package, Version, and Architecture through dpkg-deb.
func (i *Inspector) inspectDEB(ctx context.Context, packagePath string) (pkgrepo.PackageMetadata, error) {
	output, err := i.runDocker(ctx, packagePath, i.debianImage,
		"dpkg-deb", "--field", containerPackagePath, "Package", "Version", "Architecture",
	)
	if err != nil {
		return pkgrepo.PackageMetadata{}, fmt.Errorf("inspect DEB: %w", err)
	}
	fields, err := parseLabeledFields(output, []string{"Package", "Version", "Architecture"})
	if err != nil {
		return pkgrepo.PackageMetadata{}, fmt.Errorf("parse DEB metadata: %w", err)
	}

	return normalizeMetadata(fields["Package"], fields["Version"], fields["Architecture"], pkgrepo.FormatDEB)
}

// inspectRPM queries name, version, release, and architecture through rpm.
func (i *Inspector) inspectRPM(ctx context.Context, packagePath string) (pkgrepo.PackageMetadata, error) {
	output, err := i.runDocker(ctx, packagePath, i.fedoraImage,
		"rpm", "-qp", "--queryformat", "%{NAME}\n%{VERSION}\n%{RELEASE}\n%{ARCH}\n", containerPackagePath,
	)
	if err != nil {
		return pkgrepo.PackageMetadata{}, fmt.Errorf("inspect RPM: %w", err)
	}
	lines := nonemptyLines(output)
	if len(lines) != rpmMetadataFieldCount {
		return pkgrepo.PackageMetadata{}, fmt.Errorf("parse RPM metadata: got %d fields, want 4", len(lines))
	}
	if lines[2] != "1" {
		return pkgrepo.PackageMetadata{}, fmt.Errorf("parse RPM metadata: release %q is unsupported, want 1", lines[2])
	}

	return normalizeMetadata(lines[0], lines[1], lines[3], pkgrepo.FormatRPM)
}

// runDocker runs one fixed networkless read-only package inspection command.
func (i *Inspector) runDocker(ctx context.Context, packagePath, image string, command ...string) (string, error) {
	var stdout bytes.Buffer
	args := []string{
		runArgument,
		removeArgument,
		networkNoneArgument,
		readOnlyArgument,
		temporaryFilesystemArgument,
		temporaryDirectory,
		"-v", packagePath + ":" + containerPackagePath + ":ro",
		image,
	}
	args = append(args, command...)
	err := execx.Run(ctx, execx.Command{
		Program: defaultDockerPath,
		Path:    i.dockerPath,
		Args:    args,
		Env:     i.environ,
		Stdout:  &stdout,
		Stderr:  i.stderr,
	})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", commandError(err)
	}

	return stdout.String(), nil
}

// inspectAPK reads the first unique .PKGINFO member across concatenated gzip streams.
func inspectAPK(packagePath string) (pkgrepo.PackageMetadata, error) {
	file, openErr := os.Open(packagePath)
	if openErr != nil {
		return pkgrepo.PackageMetadata{}, fmt.Errorf("open APK: %w", openErr)
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	var metadata []byte
	for stream := 0; ; stream++ {
		gzipReader, gzipErr := gzip.NewReader(reader)
		if errors.Is(gzipErr, io.EOF) {
			break
		}
		if gzipErr != nil {
			return pkgrepo.PackageMetadata{}, fmt.Errorf("open APK gzip stream %d: %w", stream, gzipErr)
		}
		gzipReader.Multistream(false)
		found, streamErr := readAPKStream(gzipReader)
		if streamErr != nil {
			return pkgrepo.PackageMetadata{}, fmt.Errorf("read APK stream %d: %w", stream, streamErr)
		}
		if found != nil {
			if metadata != nil {
				return pkgrepo.PackageMetadata{}, errors.New("APK contains multiple .PKGINFO members")
			}
			metadata = found
		}
	}
	if metadata == nil {
		return pkgrepo.PackageMetadata{}, errors.New("APK does not contain .PKGINFO")
	}

	fields, err := parseAPKFields(metadata)
	if err != nil {
		return pkgrepo.PackageMetadata{}, err
	}
	return normalizeMetadata(fields["pkgname"], fields["pkgver"], fields["arch"], pkgrepo.FormatAPK)
}

// readAPKStream bounds, scans, drains, and closes one APK gzip member.
func readAPKStream(reader *gzip.Reader) ([]byte, error) {
	limited := &io.LimitedReader{R: reader, N: maxAPKStreamSize + 1}
	found, streamErr := readPackageInfo(tar.NewReader(limited))
	if streamErr == nil {
		_, streamErr = io.Copy(io.Discard, limited)
	}
	if streamErr == nil && limited.N == 0 {
		streamErr = fmt.Errorf("decompressed APK stream exceeds %d bytes", maxAPKStreamSize)
	}
	closeErr := reader.Close()
	if streamErr != nil {
		return nil, errors.Join(streamErr, closeErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close APK gzip stream: %w", closeErr)
	}

	return found, nil
}

// readPackageInfo scans one tar stream for a regular .PKGINFO member.
func readPackageInfo(reader *tar.Reader) ([]byte, error) {
	var content []byte
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return content, nil
		}
		if err != nil {
			return nil, err
		}
		if header.Name != packageInfoFile {
			continue
		}
		if content != nil {
			return nil, errors.New("tar stream contains multiple .PKGINFO members")
		}
		if !header.FileInfo().Mode().IsRegular() {
			return nil, errors.New(".PKGINFO is not a regular file")
		}
		content, err = io.ReadAll(io.LimitReader(reader, 64*1024+1))
		if err != nil {
			return nil, fmt.Errorf("read .PKGINFO: %w", err)
		}
		if len(content) > 64*1024 {
			return nil, errors.New(".PKGINFO exceeds 64 KiB")
		}
	}
}

// parseAPKFields extracts the required unique scalar fields from .PKGINFO.
func parseAPKFields(content []byte) (map[string]string, error) {
	required := map[string]struct{}{"pkgname": {}, "pkgver": {}, "arch": {}}
	fields := make(map[string]string, len(required))
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		name, value, found := strings.Cut(scanner.Text(), " = ")
		if !found {
			continue
		}
		if _, wanted := required[name]; !wanted {
			continue
		}
		if _, duplicate := fields[name]; duplicate {
			return nil, fmt.Errorf("APK field %q is duplicated", name)
		}
		fields[name] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan .PKGINFO: %w", err)
	}
	for name := range required {
		if fields[name] == "" {
			return nil, fmt.Errorf("APK field %q is missing or empty", name)
		}
	}

	return fields, nil
}

// parseLabeledFields parses unique "Name: value" lines for the required labels.
func parseLabeledFields(output string, names []string) (map[string]string, error) {
	required := make(map[string]struct{}, len(names))
	for _, name := range names {
		required[name] = struct{}{}
	}
	fields := make(map[string]string, len(names))
	for _, line := range nonemptyLines(output) {
		name, value, found := strings.Cut(line, ": ")
		if !found {
			return nil, fmt.Errorf("malformed field line %q", line)
		}
		if _, wanted := required[name]; !wanted {
			return nil, fmt.Errorf("unexpected field %q", name)
		}
		if _, duplicate := fields[name]; duplicate {
			return nil, fmt.Errorf("field %q is duplicated", name)
		}
		fields[name] = value
	}
	for _, name := range names {
		if fields[name] == "" {
			return nil, fmt.Errorf("field %q is missing or empty", name)
		}
	}

	return fields, nil
}

// nonemptyLines trims output and returns its nonempty lines in order.
func nonemptyLines(output string) []string {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

// normalizeMetadata validates shared names, stable versions, and native architectures.
func normalizeMetadata(
	nameText, versionText, architectureText string,
	format pkgrepo.Format,
) (pkgrepo.PackageMetadata, error) {
	name, err := pkgrepo.ParsePackageName(nameText)
	if err != nil {
		return pkgrepo.PackageMetadata{}, err
	}
	version, err := rel.ParseVersion(versionText)
	if err != nil {
		return pkgrepo.PackageMetadata{}, fmt.Errorf("package version: %w", err)
	}
	architecture, err := normalizeArchitecture(format, architectureText)
	if err != nil {
		return pkgrepo.PackageMetadata{}, err
	}

	return pkgrepo.PackageMetadata{Name: name, Version: version, Architecture: architecture}, nil
}

// normalizeArchitecture maps native package architecture names onto repository values.
func normalizeArchitecture(format pkgrepo.Format, value string) (pkgrepo.Architecture, error) {
	switch format {
	case pkgrepo.FormatDEB:
		switch value {
		case "amd64":
			return pkgrepo.ArchitectureAMD64, nil
		case "arm64":
			return pkgrepo.ArchitectureARM64, nil
		}
	case pkgrepo.FormatRPM, pkgrepo.FormatAPK:
		switch value {
		case "x86_64":
			return pkgrepo.ArchitectureAMD64, nil
		case "aarch64":
			return pkgrepo.ArchitectureARM64, nil
		}
	}

	return "", fmt.Errorf("%s architecture %q is unsupported", format, value)
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
