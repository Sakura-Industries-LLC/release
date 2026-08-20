package image

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePlatform(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    Platform
		wantErr string
	}{
		{
			name:  "linux amd64",
			input: "linux/amd64",
			want:  PlatformAMD64,
		},
		{
			name:  "linux arm64",
			input: "linux/arm64",
			want:  PlatformARM64,
		},
		{
			name:    "empty",
			input:   "",
			wantErr: `platform "" is not linux/amd64 or linux/arm64`,
		},
		{
			name:    "unknown linux architecture",
			input:   "linux/riscv64",
			wantErr: `platform "linux/riscv64" is not linux/amd64 or linux/arm64`,
		},
		{
			name:    "darwin amd64",
			input:   "darwin/amd64",
			wantErr: `platform "darwin/amd64" is not linux/amd64 or linux/arm64`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParsePlatform(test.input)
			if test.wantErr != "" {
				require.EqualError(t, err, test.wantErr)
				assert.Empty(t, got)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.want, got)
			assert.Equal(t, test.input, got.String())
		})
	}
}

func TestPlatformAPKArch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		platform Platform
		want     APKArch
	}{
		{
			name:     "amd64 maps to x86_64",
			platform: PlatformAMD64,
			want:     ArchX8664,
		},
		{
			name:     "arm64 maps to aarch64",
			platform: PlatformARM64,
			want:     ArchAArch64,
		},
		{
			name:     "unknown platform is the zero architecture",
			platform: Platform("linux/riscv64"),
			want:     "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := test.platform.APKArch()
			assert.Equal(t, test.want, got)
			assert.Equal(t, test.want.String(), got.String())
		})
	}
}
