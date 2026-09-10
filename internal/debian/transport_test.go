package debian

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// scriptPath locates the transport relative to this package.
	scriptPath = "../../.github/actions/publish-forgejo-debian/publish-debian.sh"
	// testPackage is the Debian package name the fixtures declare.
	testPackage = "dntls-resolver"
	// testVersion is the stable version the fixtures declare.
	testVersion = "1.2.3"
	// testOwner is the registry owner the fixtures publish to.
	testOwner = "dntls-downloads"
	// testDistribution is the apt distribution the fixtures publish to.
	testDistribution = "stable"
	// testComponent is the apt component the fixtures publish to.
	testComponent = "main"
	// testUsername is the publisher account.
	testUsername = "dntls-publisher"
	// testToken is the publisher credential. No output may contain it.
	testToken = "forgejo-publisher-token-value"
	// uploadPath is the only path a publication may PUT to.
	uploadPath = "/api/packages/" + testOwner + "/debian/pool/" + testDistribution + "/" + testComponent + "/upload"
	// poolPath is the canonical location of a published file.
	poolPath = "/api/packages/" + testOwner + "/debian/pool/" + testDistribution + "/" + testComponent + "/"
)

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

// buildPackage writes a real Debian package into dist and returns its bytes.
func buildPackage(t *testing.T, dist, name, version, architecture, payload string) []byte {
	t.Helper()

	root := filepath.Join(t.TempDir(), "root")
	control := filepath.Join(root, "DEBIAN")
	require.NoError(t, os.MkdirAll(control, 0o755))
	require.NoError(t, os.Chmod(root, 0o755))
	require.NoError(t, os.Chmod(control, 0o755))

	fields := fmt.Sprintf(
		"Package: %s\nVersion: %s\nArchitecture: %s\nMaintainer: Release Tests <tests@example.invalid>\nDescription: transport fixture\n",
		name,
		version,
		architecture,
	)
	require.NoError(t, os.WriteFile(filepath.Join(control, "control"), []byte(fields), 0o644))

	share := filepath.Join(root, "usr", "share", name)
	require.NoError(t, os.MkdirAll(share, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(share, "payload"), []byte(payload), 0o644))

	built := filepath.Join(dist, fmt.Sprintf("%s_%s_%s.deb", name, version, architecture))
	command := exec.Command("dpkg-deb", "--build", "--root-owner-group", root, built)
	output, err := command.CombinedOutput()
	require.NoError(t, err, "dpkg-deb: %s", output)

	content, err := os.ReadFile(built)
	require.NoError(t, err)

	return content
}

// completeSet writes the amd64 and arm64 packages a publication expects.
func completeSet(t *testing.T) (string, map[string][]byte) {
	t.Helper()

	dist := t.TempDir()
	content := map[string][]byte{
		testPackage + "_" + testVersion + "_amd64.deb": buildPackage(
			t,
			dist,
			testPackage,
			testVersion,
			"amd64",
			"amd64 payload",
		),
		testPackage + "_" + testVersion + "_arm64.deb": buildPackage(
			t,
			dist,
			testPackage,
			testVersion,
			"arm64",
			"arm64 payload",
		),
	}

	return dist, content
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
		"FORGEJO_DISTRIBUTION":     testDistribution,
		"FORGEJO_COMPONENT":        testComponent,
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

// requireTools skips when the host cannot build or move Debian packages.
func requireTools(t *testing.T) {
	t.Helper()

	for _, tool := range []string{"bash", "curl", "dpkg-deb"} {
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

	fake := newRegistry(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusCreated)
	})
	dist, content := completeSet(t)

	result := run(t, dist, fake.server.URL, fake.trust(t), nil)
	require.NoError(t, result.err, "stdout: %s stderr: %s", result.stdout, result.stderr)

	calls := fake.recorded()
	require.Len(t, calls, 2)
	uploaded := make(map[string]bool, len(calls))
	for _, received := range calls {
		assert.Equal(t, http.MethodPut, received.method)
		assert.Equal(t, uploadPath, received.path)
		assert.Equal(t, testUsername, received.user)
		assert.Equal(t, testToken, received.password)
		uploaded[digest(received.body)] = true
	}
	for name, bytes := range content {
		assert.True(t, uploaded[digest(bytes)], "%s was not uploaded", name)
		assert.Contains(t, result.stdout, "published "+name)
	}

	assert.NotContains(t, result.stdout, testToken)
	assert.NotContains(t, result.stderr, testToken)
}

func TestPublishAcceptsAnIdenticalPublishedFile(t *testing.T) {
	t.Parallel()
	requireTools(t)

	dist, content := completeSet(t)
	fake := newRegistry(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPut {
			http.Error(writer, "file already exists", http.StatusConflict)

			return
		}
		published, known := content[strings.TrimPrefix(request.URL.Path, poolPath)]
		if !known {
			http.Error(writer, "not found", http.StatusNotFound)

			return
		}
		_, _ = writer.Write(published)
	})

	result := run(t, dist, fake.server.URL, fake.trust(t), nil)
	require.NoError(t, result.err, "stdout: %s stderr: %s", result.stdout, result.stderr)

	for name := range content {
		assert.Contains(t, result.stdout, "unchanged "+name)
	}
	for _, received := range fake.recorded() {
		assert.NotEqual(t, http.MethodDelete, received.method)
		if received.method == http.MethodGet {
			assert.True(t, strings.HasPrefix(received.path, poolPath), "unexpected read of %s", received.path)
		}
	}
}

func TestPublishRefusesADifferentPublishedFile(t *testing.T) {
	t.Parallel()
	requireTools(t)

	dist, _ := completeSet(t)
	fake := newRegistry(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPut {
			http.Error(writer, "file already exists", http.StatusConflict)

			return
		}
		_, _ = writer.Write([]byte("a different build"))
	})

	result := run(t, dist, fake.server.URL, fake.trust(t), nil)
	require.Error(t, result.err)
	assert.Contains(t, result.stderr, "already published")
	assert.Contains(t, result.stderr, digest([]byte("a different build")))
	assert.NotContains(t, result.stderr, testToken)

	for _, received := range fake.recorded() {
		assert.NotEqual(t, http.MethodDelete, received.method)
	}
}

func TestPublishNeverFollowsARedirect(t *testing.T) {
	t.Parallel()
	requireTools(t)

	elsewhere := newRegistry(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})
	dist, _ := completeSet(t)
	fake := newRegistry(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPut {
			http.Error(writer, "file already exists", http.StatusConflict)

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

	fake := newRegistry(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusCreated)
	})
	dist, _ := completeSet(t)

	result := run(t, dist, fake.server.URL, fake.trust(t), map[string]string{
		"FORGEJO_PUBLISH": "false",
		"FORGEJO_TOKEN":   "",
	})
	require.NoError(t, result.err, "stdout: %s stderr: %s", result.stdout, result.stderr)
	assert.Contains(t, result.stdout, "verified "+testPackage+" "+testVersion)
	assert.Empty(t, fake.recorded())
}

func TestPublishRejectsAVersionTheReleaseDoesNotName(t *testing.T) {
	t.Parallel()
	requireTools(t)

	tests := []struct {
		name    string
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

			fake := newRegistry(t, func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusCreated)
			})
			dist, _ := completeSet(t)

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
		name    string
		arrange func(t *testing.T, dist string)
	}{
		{
			name: "one architecture",
			arrange: func(t *testing.T, dist string) {
				buildPackage(t, dist, testPackage, testVersion, "amd64", "amd64 payload")
			},
		},
		{
			name: "the same architecture twice",
			arrange: func(t *testing.T, dist string) {
				buildPackage(t, dist, testPackage, testVersion, "amd64", "amd64 payload")
				second := buildPackage(t, dist, testPackage, testVersion, "amd64", "amd64 payload")
				require.NoError(t, os.WriteFile(filepath.Join(dist, "second.deb"), second, 0o644))
			},
		},
		{
			name: "mismatched versions",
			arrange: func(t *testing.T, dist string) {
				buildPackage(t, dist, testPackage, testVersion, "amd64", "amd64 payload")
				buildPackage(t, dist, testPackage, "1.2.4", "arm64", "arm64 payload")
			},
		},
		{
			name: "a prerelease version",
			arrange: func(t *testing.T, dist string) {
				buildPackage(t, dist, testPackage, "1.2.3~rc1", "amd64", "amd64 payload")
				buildPackage(t, dist, testPackage, "1.2.3~rc1", "arm64", "arm64 payload")
			},
		},
		{
			name: "another package",
			arrange: func(t *testing.T, dist string) {
				buildPackage(t, dist, "dntls", testVersion, "amd64", "amd64 payload")
				buildPackage(t, dist, "dntls", testVersion, "arm64", "arm64 payload")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := newRegistry(t, func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusCreated)
			})
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
		name      string
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
			name:      "component escaping its path segment",
			overrides: map[string]string{"FORGEJO_COMPONENT": "main/../../../attacker"},
		},
		{
			name:      "publishing without a token",
			overrides: map[string]string{"FORGEJO_TOKEN": ""},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := newRegistry(t, func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusCreated)
			})
			dist, _ := completeSet(t)

			result := run(t, dist, fake.server.URL, fake.trust(t), test.overrides)
			require.Error(t, result.err, "stdout: %s", result.stdout)
			assert.NotContains(t, result.stderr, testToken)
			assert.Empty(t, fake.recorded())
		})
	}
}
