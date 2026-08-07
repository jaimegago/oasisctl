package profile_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jaimegago/oasisctl/internal/profile"
)

// TestLoad_ProfileMetadataFromListItemHeader guards a silent failure mode.
//
// SI profile 0.3.0-rc1 reflowed profile.md's header from bare bold lines
// (`**Version:** 0.2.0-rc3`) into markdown list items (`- **Version:** 0.3.0-rc1`).
// The loader matched on a line-leading bold marker, so every metadata field
// parsed to the empty string and the report header's profile version went blank
// without any error being raised.
func TestLoad_ProfileMetadataFromListItemHeader(t *testing.T) {
	p, err := profile.NewLoader().Load(context.Background(), vendoredSpec)
	require.NoError(t, err)

	assert.Equal(t, "0.3.0-rc1", p.Metadata.Version,
		"the vendored profile's version must parse from its list-item header")
	assert.Equal(t, "Software Infrastructure", p.Metadata.Domain)
	assert.NotEmpty(t, p.Metadata.OASISCore)
	assert.Contains(t, p.Metadata.OASISCore, "1.0.0-rc1.11")
}

// TestLoad_ProfileMetadataHeaderForms confirms both header spellings parse, so
// a profile that has not reflowed its header is unaffected.
func TestLoad_ProfileMetadataHeaderForms(t *testing.T) {
	tests := []struct {
		name   string
		header string
	}{
		{
			name: "bare bold form",
			header: "# Test Profile\n\n" +
				"**Version:** 9.9.9\n" +
				"**Domain:** Testing\n" +
				"**OASIS Core Dependency:** >= 1.0.0-rc1.11\n",
		},
		{
			name: "list-item form",
			header: "# Test Profile\n\n" +
				"- **Version:** 9.9.9\n" +
				"- **Domain:** Testing\n" +
				"- **OASIS Core Dependency:** >= 1.0.0-rc1.11\n",
		},
		{
			name: "asterisk list-item form",
			header: "# Test Profile\n\n" +
				"* **Version:** 9.9.9\n" +
				"* **Domain:** Testing\n" +
				"* **OASIS Core Dependency:** >= 1.0.0-rc1.11\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "profile.md"), []byte(tt.header), 0o600))
			// The loader requires these companion documents to exist; their
			// content is irrelevant to header parsing.
			for _, name := range []string{"behavior-definitions.md", "stimulus-library.md"} {
				require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("# empty\n"), 0o600))
			}

			p, err := profile.NewLoader().Load(context.Background(), dir)
			require.NoError(t, err)
			assert.Equal(t, "9.9.9", p.Metadata.Version)
			assert.Equal(t, "Testing", p.Metadata.Domain)
			assert.Equal(t, ">= 1.0.0-rc1.11", p.Metadata.OASISCore)
		})
	}
}
