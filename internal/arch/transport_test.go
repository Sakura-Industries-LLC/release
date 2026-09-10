package arch

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// scriptPath locates the transport relative to this package.
	scriptPath = "../../.github/actions/publish-forgejo-arch/publish-arch.sh"
	// testPackage is the pkgname the fixtures declare.
	testPackage = "dntls-resolver"
	// testVersion is the stable upstream version the fixtures declare.
	testVersion = "1.2.3"
	// testPkgver is the full pkgver the fixtures declare.
	testPkgver = testVersion + "-1"
	// testOwner is the registry owner the fixtures publish to.
	testOwner = "dntls-downloads"
	// testGroup is the registry group the fixtures publish to.
	testGroup = "stable"
	// testUsername is the publisher account.
	testUsername = "dntls-publisher"
	// testToken is the publisher credential. No output may contain it.
	testToken = "forgejo-publisher-token-value"
	// uploadPath is the only path a publication may PUT to.
	uploadPath = "/api/packages/" + testOwner + "/arch/" + testGroup
	// keyPath serves the key the registry signs packages and databases with.
	keyPath = "/api/packages/" + testOwner + "/arch/repository.key"
)

// registryKey is the OpenPGP key the fake registry signs with. It stands in
// for the key Forgejo generates per owner.
var registryKey struct {
	// home is the GnuPG home directory holding the private key.
	home string
	// public is the armored public key the registry serves.
	public []byte
}

// TestMain generates the registry key once, because every published state the
// transport checks is signed with it.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "arch-registry-key")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	registryKey.home = home

	if _, err = exec.LookPath("gpg"); err == nil {
		generate := exec.Command(
			"gpg", "--homedir", home, "--batch", "--quiet", "--passphrase", "",
			"--quick-generate-key", "Forgejo Registry <registry@example.invalid>", "default", "default", "never",
		)
		if output, genErr := generate.CombinedOutput(); genErr != nil {
			fmt.Fprintf(os.Stderr, "gpg --quick-generate-key: %s\n", output)
			os.Exit(1)
		}

		export := exec.Command("gpg", "--homedir", home, "--batch", "--quiet", "--armor", "--export")
		public, expErr := export.Output()
		if expErr != nil {
			fmt.Fprintln(os.Stderr, expErr)
			os.Exit(1)
		}
		registryKey.public = public
	}

	code := m.Run()

	_ = exec.Command("gpgconf", "--homedir", home, "--kill", "all").Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}

// sign returns the detached signature the registry key makes over content.
func sign(t *testing.T, content []byte) []byte {
	t.Helper()

	input := filepath.Join(t.TempDir(), "input")
	require.NoError(t, os.WriteFile(input, content, 0o600))

	command := exec.Command(
		"gpg", "--homedir", registryKey.home, "--batch", "--quiet", "--yes",
		"--detach-sign", "--output", "-", input,
	)
	signature, err := command.Output()
	require.NoError(t, err)

	return signature
}

// call is one request the fake registry received.
type call struct {
	// method is the HTTP method.
	method string
	// path is the request path.
	path string
	// user is the basic-auth user, empty when unauthenticated.
	user string
	// password is the basic-auth password, empty when unauthenticated.
	password string
	// body is the request payload.
	body []byte
}

// registry is a fake Forgejo package registry that records every request.
type registry struct {
	// server serves HTTPS with a certificate the tests trust.
	server *httptest.Server
	// mu guards calls.
	mu sync.Mutex
	// calls holds every received request, in order.
	calls []call
}

// newRegistry starts a fake registry that delegates to handle.
func newRegistry(t *testing.T, handle http.HandlerFunc) *registry {
	t.Helper()

	fake := &registry{}
	fake.server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		assert.NoError(t, err)
		user, password, _ := request.BasicAuth()

		fake.mu.Lock()
		fake.calls = append(fake.calls, call{
			method:   request.Method,
			path:     request.URL.Path,
			user:     user,
			password: password,
			body:     body,
		})
		fake.mu.Unlock()

		handle(writer, request)
	}))
	t.Cleanup(fake.server.Close)

	return fake
}

// recorded returns the requests the registry received.
func (r *registry) recorded() []call {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]call(nil), r.calls...)
}

// trust writes the registry's certificate where curl will read it.
func (r *registry) trust(t *testing.T) string {
	t.Helper()

	encoded := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: r.server.Certificate().Raw})
	path := filepath.Join(t.TempDir(), "registry.pem")
	require.NoError(t, os.WriteFile(path, encoded, 0o600))

	return path
}

// contents is the set of files a fake registry serves, keyed by request path.
type contents map[string][]byte

// serveFiles answers uploads with status and every read from files.
func serveFiles(status int, files contents) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPut {
			writer.WriteHeader(status)

			return
		}
		body, known := files[request.URL.Path]
		if !known {
			http.Error(writer, "not found", http.StatusNotFound)

			return
		}
		_, _ = writer.Write(body)
	}
}

// buildPackage writes an Arch package into dist under an arbitrary producer
// file name and returns its bytes. The publisher must derive every registry
// path from the metadata instead, so the name is deliberately not canonical.
func buildPackage(t *testing.T, dist, name, pkgver, architecture string, mtree bool) []byte {
	t.Helper()

	root := t.TempDir()
	info := fmt.Sprintf(
		"# Generated by the transport tests\npkgname = %s\npkgbase = %s\npkgver = %s\npkgdesc = transport fixture\n"+
			"url = https://example.invalid\nbuilddate = 1700000000\npackager = Release Tests <tests@example.invalid>\n"+
			"size = 12\narch = %s\nlicense = MIT\n",
		name,
		name,
		pkgver,
		architecture,
	)
	require.NoError(t, os.WriteFile(filepath.Join(root, ".PKGINFO"), []byte(info), 0o600))

	members := []string{".PKGINFO"}
	if mtree {
		require.NoError(t, os.WriteFile(filepath.Join(root, ".MTREE"), []byte("#mtree\n"+architecture+"\n"), 0o600))
		members = append(members, ".MTREE")
	}

	payload := filepath.Join(root, "usr", "bin")
	require.NoError(t, os.MkdirAll(payload, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(payload, name), []byte(architecture+" payload"), 0o755))
	members = append(members, "usr")

	built := filepath.Join(dist, fmt.Sprintf("%s_%s_linux_%s.pkg.tar.zst", name, pkgver, architecture))
	command := exec.Command("bsdtar", append([]string{"--zstd", "-cf", built, "-C", root}, members...)...)
	output, err := command.CombinedOutput()
	require.NoError(t, err, "bsdtar: %s", output)

	content, err := os.ReadFile(built)
	require.NoError(t, err)

	return content
}

// completeSet writes the x86_64 and aarch64 packages a publication expects
// and returns the distribution directory and the packages by architecture.
func completeSet(t *testing.T) (string, map[string][]byte) {
	t.Helper()

	dist := t.TempDir()
	packages := map[string][]byte{
		"x86_64":  buildPackage(t, dist, testPackage, testPkgver, "x86_64", true),
		"aarch64": buildPackage(t, dist, testPackage, testPkgver, "aarch64", true),
	}

	return dist, packages
}

// canonicalName is the file name Forgejo derives from the package metadata.
func canonicalName(architecture string) string {
	return fmt.Sprintf("%s-%s-%s.pkg.tar.zst", testPackage, testPkgver, architecture)
}

// describe renders the database entry Forgejo generates for a package.
func describe(architecture string, content, signature []byte) string {
	fields := []string{
		"FILENAME", canonicalName(architecture),
		"NAME", testPackage,
		"BASE", testPackage,
		"VERSION", testPkgver,
		"DESC", "transport fixture",
		"CSIZE", strconv.Itoa(len(content)),
		"ISIZE", "12",
		"SHA256SUM", digest(content),
		"PGPSIG", base64.StdEncoding.EncodeToString(signature),
		"ARCH", architecture,
		"BUILDDATE", "1700000000",
		"PACKAGER", "Release Tests <tests@example.invalid>",
	}

	var body strings.Builder
	for index := 0; index < len(fields); index += 2 {
		fmt.Fprintf(&body, "%%%s%%\n%s\n\n", fields[index], fields[index+1])
	}

	return body.String()
}

// buildDatabase returns the gzipped tar Forgejo serves as the pacman database
// for one architecture, holding one entry per package name.
func buildDatabase(t *testing.T, entries map[string]string) []byte {
	t.Helper()

	var raw strings.Builder
	compressor := gzip.NewWriter(&raw)
	archive := tar.NewWriter(compressor)
	for name, description := range entries {
		require.NoError(t, archive.WriteHeader(&tar.Header{
			Name: name + "/desc",
			Size: int64(len(description)),
			Mode: 0o644,
		}))
		_, err := archive.Write([]byte(description))
		require.NoError(t, err)
	}
	require.NoError(t, archive.Close())
	require.NoError(t, compressor.Close())

	return []byte(raw.String())
}

// publishedState returns the files a registry holds after a complete
// publication of packages.
func publishedState(t *testing.T, packages map[string][]byte) contents {
	t.Helper()

	files := contents{keyPath: registryKey.public}
	for architecture, content := range packages {
		base := uploadPath + "/" + architecture
		signature := sign(t, content)
		database := buildDatabase(t, map[string]string{
			testPackage + "-" + testPkgver: describe(architecture, content, signature),
		})

		files[base+"/"+canonicalName(architecture)] = content
		files[base+"/"+canonicalName(architecture)+".sig"] = signature
		files[base+"/"+testGroup+".db"] = database
		files[base+"/"+testGroup+".db.sig"] = sign(t, database)
	}

	return files
}

// publication is one invocation of the transport.
type publication struct {
	// stdout is everything the script printed.
	stdout string
	// stderr is everything the script reported.
	stderr string
	// err is nil when the script exited zero.
	err error
}

// run executes the transport against dist with the given environment.
//
// An override value of the empty string removes that variable.
func run(t *testing.T, dist, origin, caBundle string, overrides map[string]string) publication {
	t.Helper()

	values := map[string]string{
		"FORGEJO_ORIGIN":           origin,
		"FORGEJO_OWNER":            testOwner,
		"FORGEJO_GROUP":            testGroup,
		"FORGEJO_PACKAGE":          testPackage,
		"FORGEJO_EXPECTED_VERSION": testVersion,
		"FORGEJO_USERNAME":         testUsername,
		"FORGEJO_PUBLISH":          "true",
		"FORGEJO_TOKEN":            testToken,
	}
	for name, value := range overrides {
		if value == "" {
			delete(values, name)

			continue
		}
		values[name] = value
	}

	script, err := filepath.Abs(scriptPath)
	require.NoError(t, err)

	command := exec.Command("bash", script, dist)
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"CURL_CA_BUNDLE=" + caBundle,
	}
	for name, value := range values {
		command.Env = append(command.Env, name+"="+value)
	}

	var stdout, stderr strings.Builder
	command.Stdout = &stdout
	command.Stderr = &stderr
	runErr := command.Run()

	return publication{stdout: stdout.String(), stderr: stderr.String(), err: runErr}
}

// requireTools skips when the host cannot build or verify Arch packages.
func requireTools(t *testing.T) {
	t.Helper()

	for _, tool := range []string{"bash", "curl", "bsdtar", "zstd", "gpg", "gpgv", "openssl"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
}

// digest returns the lowercase SHA-256 hex of content.
func digest(content []byte) string {
	sum := sha256.Sum256(content)

	return hex.EncodeToString(sum[:])
}

func TestPublishUploadsEveryArchitectureAndLeaksNothing(t *testing.T) {
	t.Parallel()
	requireTools(t)

	dist, packages := completeSet(t)
	fake := newRegistry(t, serveFiles(http.StatusCreated, publishedState(t, packages)))

	result := run(t, dist, fake.server.URL, fake.trust(t), nil)
	require.NoError(t, result.err, "stdout: %s stderr: %s", result.stdout, result.stderr)

	uploaded := make(map[string]bool, len(packages))
	read := make(map[string]bool, len(packages)*4)
	for _, received := range fake.recorded() {
		if received.method == http.MethodGet {
			read[received.path] = true

			continue
		}
		assert.Equal(t, http.MethodPut, received.method)
		assert.Equal(t, uploadPath, received.path)
		assert.Equal(t, testUsername, received.user)
		assert.Equal(t, testToken, received.password)
		uploaded[digest(received.body)] = true
	}
	require.Len(t, uploaded, len(packages))
	for architecture, content := range packages {
		assert.True(t, uploaded[digest(content)], "%s was not uploaded", architecture)
		// The producer file name is not the registry file name: the publisher
		// reports and addresses what the metadata declares.
		assert.Contains(t, result.stdout, "published "+canonicalName(architecture))

		// An accepted upload is only a publication once the registry serves
		// the package, its signature, and a signed database that indexes it.
		base := uploadPath + "/" + architecture
		for _, path := range []string{
			base + "/" + canonicalName(architecture),
			base + "/" + canonicalName(architecture) + ".sig",
			base + "/" + testGroup + ".db",
			base + "/" + testGroup + ".db.sig",
		} {
			assert.True(t, read[path], "%s was never read back", path)
		}
	}

	assert.NotContains(t, result.stdout, testToken)
	assert.NotContains(t, result.stderr, testToken)
}

func TestPublishAcceptsAFullyPublishedPackage(t *testing.T) {
	t.Parallel()
	requireTools(t)

	dist, packages := completeSet(t)
	fake := newRegistry(t, serveFiles(http.StatusConflict, publishedState(t, packages)))

	result := run(t, dist, fake.server.URL, fake.trust(t), nil)
	require.NoError(t, result.err, "stdout: %s stderr: %s", result.stdout, result.stderr)

	for architecture := range packages {
		assert.Contains(t, result.stdout, "unchanged "+canonicalName(architecture))
	}
	for _, received := range fake.recorded() {
		assert.NotEqual(t, http.MethodDelete, received.method)
	}
}

func TestPublishRefusesADifferentPublishedFile(t *testing.T) {
	t.Parallel()
	requireTools(t)

	dist, packages := completeSet(t)
	files := publishedState(t, packages)
	files[uploadPath+"/x86_64/"+canonicalName("x86_64")] = []byte("a different build")
	fake := newRegistry(t, serveFiles(http.StatusConflict, files))

	result := run(t, dist, fake.server.URL, fake.trust(t), nil)
	require.Error(t, result.err)
	assert.Contains(t, result.stderr, digest([]byte("a different build")))
	assert.NotContains(t, result.stderr, testToken)

	for _, received := range fake.recorded() {
		assert.NotEqual(t, http.MethodDelete, received.method)
	}
}

// A Forgejo Arch upload is not atomic: the instance creates the package, then
// signs it, then rebuilds and signs the database. A failure between those
// steps leaves state a retry meets as 409, so every incomplete state must
// fail rather than report the release as published.
func TestPublishRefusesAnIncompletelyPublishedPackage(t *testing.T) {
	t.Parallel()
	requireTools(t)

	tests := []struct {
		// name describes the state the registry was left in.
		name string
		// damage turns a complete published state into that state.
		damage func(t *testing.T, files contents, packages map[string][]byte)
	}{
		{
			name: "the package signature was never written",
			damage: func(_ *testing.T, files contents, _ map[string][]byte) {
				delete(files, uploadPath+"/x86_64/"+canonicalName("x86_64")+".sig")
			},
		},
		{
			name: "the package signature covers other bytes",
			damage: func(t *testing.T, files contents, _ map[string][]byte) {
				files[uploadPath+"/x86_64/"+canonicalName("x86_64")+".sig"] = sign(t, []byte("another package"))
			},
		},
		{
			name: "the database was never rebuilt",
			damage: func(_ *testing.T, files contents, _ map[string][]byte) {
				delete(files, uploadPath+"/x86_64/"+testGroup+".db")
			},
		},
		{
			name: "the database signature covers other bytes",
			damage: func(t *testing.T, files contents, _ map[string][]byte) {
				files[uploadPath+"/x86_64/"+testGroup+".db.sig"] = sign(t, []byte("another database"))
			},
		},
		{
			name: "the database still indexes an older version",
			damage: func(t *testing.T, files contents, packages map[string][]byte) {
				content := packages["x86_64"]
				stale := buildDatabase(t, map[string]string{
					testPackage + "-1.2.2-1": describe("x86_64", content, sign(t, content)),
				})
				files[uploadPath+"/x86_64/"+testGroup+".db"] = stale
				files[uploadPath+"/x86_64/"+testGroup+".db.sig"] = sign(t, stale)
			},
		},
		{
			name: "the database indexes no version of this package",
			damage: func(t *testing.T, files contents, packages map[string][]byte) {
				content := packages["x86_64"]
				other := buildDatabase(t, map[string]string{
					"other-package-1.2.3-1": describe("x86_64", content, sign(t, content)),
				})
				files[uploadPath+"/x86_64/"+testGroup+".db"] = other
				files[uploadPath+"/x86_64/"+testGroup+".db.sig"] = sign(t, other)
			},
		},
		{
			name: "the database entry addresses other bytes",
			damage: func(t *testing.T, files contents, _ map[string][]byte) {
				signature := files[uploadPath+"/x86_64/"+canonicalName("x86_64")+".sig"]
				wrong := buildDatabase(t, map[string]string{
					testPackage + "-" + testPkgver: describe("x86_64", []byte("another package"), signature),
				})
				files[uploadPath+"/x86_64/"+testGroup+".db"] = wrong
				files[uploadPath+"/x86_64/"+testGroup+".db.sig"] = sign(t, wrong)
			},
		},
		{
			name: "the database entry carries a signature the registry does not serve",
			damage: func(t *testing.T, files contents, packages map[string][]byte) {
				content := packages["x86_64"]
				wrong := buildDatabase(t, map[string]string{
					testPackage + "-" + testPkgver: describe("x86_64", content, sign(t, []byte("another package"))),
				})
				files[uploadPath+"/x86_64/"+testGroup+".db"] = wrong
				files[uploadPath+"/x86_64/"+testGroup+".db.sig"] = sign(t, wrong)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dist, packages := completeSet(t)
			files := publishedState(t, packages)
			test.damage(t, files, packages)
			fake := newRegistry(t, serveFiles(http.StatusConflict, files))

			result := run(t, dist, fake.server.URL, fake.trust(t), nil)
			require.Error(t, result.err, "stdout: %s", result.stdout)
			assert.NotContains(t, result.stdout, canonicalName("x86_64"))
			for _, received := range fake.recorded() {
				assert.NotEqual(t, http.MethodDelete, received.method)
			}
		})
	}
}

func TestPublishNeverFollowsARedirect(t *testing.T) {
	t.Parallel()
	requireTools(t)

	dist, packages := completeSet(t)
	files := publishedState(t, packages)
	elsewhere := newRegistry(t, serveFiles(http.StatusOK, files))
	fake := newRegistry(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPut {
			writer.WriteHeader(http.StatusCreated)

			return
		}
		http.Redirect(writer, request, elsewhere.server.URL+request.URL.Path, http.StatusFound)
	})

	result := run(t, dist, fake.server.URL, fake.trust(t), nil)
	require.Error(t, result.err)
	assert.Contains(t, result.stderr, "HTTP 302")
	assert.Empty(t, elsewhere.recorded(), "the credentialed client followed a redirect")
}

func TestVerifyOnlyChecksTheSetWithoutContactingTheRegistry(t *testing.T) {
	t.Parallel()
	requireTools(t)

	dist, packages := completeSet(t)
	fake := newRegistry(t, serveFiles(http.StatusCreated, publishedState(t, packages)))

	result := run(t, dist, fake.server.URL, fake.trust(t), map[string]string{
		"FORGEJO_PUBLISH": "false",
		"FORGEJO_TOKEN":   "",
	})
	require.NoError(t, result.err, "stdout: %s stderr: %s", result.stdout, result.stderr)
	assert.Contains(t, result.stdout, "verified "+testPackage+" "+testPkgver)
	assert.Empty(t, fake.recorded())
}

func TestPublishRejectsAVersionTheReleaseDoesNotName(t *testing.T) {
	t.Parallel()
	requireTools(t)

	tests := []struct {
		// name describes the version the caller supplied.
		name string
		// version is the release version the publisher is told to publish.
		version string
	}{
		{
			name:    "a version from another release",
			version: "9.9.9",
		},
		{
			name:    "no release version at all",
			version: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dist, packages := completeSet(t)
			fake := newRegistry(t, serveFiles(http.StatusCreated, publishedState(t, packages)))

			result := run(t, dist, fake.server.URL, fake.trust(t), map[string]string{
				"FORGEJO_EXPECTED_VERSION": test.version,
			})
			require.Error(t, result.err, "stdout: %s", result.stdout)
			assert.Empty(t, fake.recorded())
		})
	}
}

func TestPublishRejectsAnIncompletePackageSet(t *testing.T) {
	t.Parallel()
	requireTools(t)

	tests := []struct {
		// name describes the set the producer built.
		name string
		// arrange writes that set into dist.
		arrange func(t *testing.T, dist string)
	}{
		{
			name: "one architecture",
			arrange: func(t *testing.T, dist string) {
				buildPackage(t, dist, testPackage, testPkgver, "x86_64", true)
			},
		},
		{
			name: "the same architecture twice",
			arrange: func(t *testing.T, dist string) {
				buildPackage(t, dist, testPackage, testPkgver, "x86_64", true)
				second := buildPackage(t, dist, testPackage, testPkgver, "x86_64", true)
				require.NoError(t, os.WriteFile(filepath.Join(dist, "second.pkg.tar.zst"), second, 0o600))
			},
		},
		{
			name: "mismatched versions",
			arrange: func(t *testing.T, dist string) {
				buildPackage(t, dist, testPackage, testPkgver, "x86_64", true)
				buildPackage(t, dist, testPackage, "1.2.3-2", "aarch64", true)
			},
		},
		{
			name: "a prerelease version",
			arrange: func(t *testing.T, dist string) {
				buildPackage(t, dist, testPackage, "1.2.3rc1-1", "x86_64", true)
				buildPackage(t, dist, testPackage, "1.2.3rc1-1", "aarch64", true)
			},
		},
		{
			name: "another package",
			arrange: func(t *testing.T, dist string) {
				buildPackage(t, dist, "dntls", testPkgver, "x86_64", true)
				buildPackage(t, dist, "dntls", testPkgver, "aarch64", true)
			},
		},
		{
			name: "a package the registry would reject for lacking an .MTREE",
			arrange: func(t *testing.T, dist string) {
				buildPackage(t, dist, testPackage, testPkgver, "x86_64", false)
				buildPackage(t, dist, testPackage, testPkgver, "aarch64", true)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := newRegistry(t, serveFiles(http.StatusCreated, contents{keyPath: registryKey.public}))
			dist := t.TempDir()
			test.arrange(t, dist)

			result := run(t, dist, fake.server.URL, fake.trust(t), nil)
			require.Error(t, result.err, "stdout: %s", result.stdout)
			assert.Empty(t, fake.recorded(), "a rejected set still reached the registry")
		})
	}
}

func TestPublishRejectsAnUnsafeDestination(t *testing.T) {
	t.Parallel()
	requireTools(t)

	tests := []struct {
		// name describes the destination the caller supplied.
		name string
		// overrides are the environment changes that describe it.
		overrides map[string]string
	}{
		{
			name:      "plaintext origin",
			overrides: map[string]string{"FORGEJO_ORIGIN": "http://forge.example.net"},
		},
		{
			name:      "origin carrying a path",
			overrides: map[string]string{"FORGEJO_ORIGIN": "https://forge.example.net/api"},
		},
		{
			name:      "origin carrying credentials",
			overrides: map[string]string{"FORGEJO_ORIGIN": "https://publisher:" + testToken + "@forge.example.net"},
		},
		{
			name:      "owner escaping its path segment",
			overrides: map[string]string{"FORGEJO_OWNER": "dntls-downloads/../../attacker"},
		},
		{
			name:      "group escaping its path segment",
			overrides: map[string]string{"FORGEJO_GROUP": "stable/../../../attacker"},
		},
		{
			name:      "publishing without a token",
			overrides: map[string]string{"FORGEJO_TOKEN": ""},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dist, packages := completeSet(t)
			fake := newRegistry(t, serveFiles(http.StatusCreated, publishedState(t, packages)))

			result := run(t, dist, fake.server.URL, fake.trust(t), test.overrides)
			require.Error(t, result.err, "stdout: %s", result.stdout)
			assert.NotContains(t, result.stderr, testToken)
			assert.Empty(t, fake.recorded())
		})
	}
}
