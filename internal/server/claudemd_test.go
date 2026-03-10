package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseAtReferences(t *testing.T) {
	// Create a temp directory with a referenced file
	tmpDir := t.TempDir()
	refFile := filepath.Join(tmpDir, "RTK.md")
	if err := os.WriteFile(refFile, []byte("# RTK content"), 0644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		content string
		want    int
	}{
		{
			name:    "single @reference",
			content: "@RTK.md",
			want:    1,
		},
		{
			name:    "no references",
			content: "# Just a heading\nSome text",
			want:    0,
		},
		{
			name:    "reference to nonexistent file",
			content: "@nonexistent.md",
			want:    0,
		},
		{
			name:    "reference with surrounding text",
			content: "# Title\n@RTK.md\nSome other text",
			want:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			refs := parseAtReferences(tt.content, tmpDir)
			if len(refs) != tt.want {
				t.Errorf("parseAtReferences() returned %d refs, want %d", len(refs), tt.want)
			}
		})
	}
}
