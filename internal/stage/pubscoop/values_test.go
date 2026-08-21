package pubscoop_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/release/internal/stage/pubscoop"
)

// TestParseManifestNameAcceptsSafeNames proves the filename stem stays
// confined to lowercase letters, digits, and interior hyphens.
func TestParseManifestNameAcceptsSafeNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		// name identifies the case.
		name string
		// value is the candidate manifest name.
		value string
	}{
		{name: "single letter", value: "a"},
		{name: "single digit", value: "1"},
		{name: "hyphenated token", value: "release-cli"},
		{name: "interior hyphens", value: "rel-ease-cli"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := pubscoop.ParseManifestName(tt.value)
			require.NoError(t, err)
			assert.Equal(t, tt.value, got.String())
			assert.Equal(t, tt.value+".json", got.Path().String())
		})
	}
}

// TestParseManifestNameRejectsUnsafeNames proves empty, decorated, and
// uppercase names never become a writable path.
func TestParseManifestNameRejectsUnsafeNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		// name identifies the case.
		name string
		// value is the rejected manifest name.
		value string
	}{
		{name: "empty", value: ""},
		{name: "uppercase", value: "Release-CLI"},
		{name: "leading hyphen", value: "-release"},
		{name: "trailing hyphen", value: "release-"},
		{name: "underscore", value: "release_cli"},
		{name: "dot", value: "release.cli"},
		{name: "slash", value: "release/cli"},
		{name: "space", value: "release cli"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := pubscoop.ParseManifestName(tt.value)
			require.Error(t, err)
			if tt.value == "" {
				assert.Contains(t, err.Error(), "is empty")
				return
			}
			assert.Contains(t, err.Error(), "lowercase letters, digits, and interior hyphens")
		})
	}
}

// TestParseRepositoryRejectsInvalidOwnerName proves the bucket coordinate
// stays in owner/name form.
func TestParseRepositoryRejectsInvalidOwnerName(t *testing.T) {
	t.Parallel()

	_, err := pubscoop.ParseRepository("not-a-repo")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be owner/name")
}
