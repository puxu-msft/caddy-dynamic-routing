package caddyslb

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocs_NoFencedBlocksOutsideExamples(t *testing.T) {
	skipDirs := map[string]struct{}{
		".git":     {},
		"examples": {},
	}

	var offenders []string
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if _, ok := skipDirs[d.Name()]; ok {
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(path, ".md") {
			return nil
		}

		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(b), "```") {
			offenders = append(offenders, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking markdown files: %v", err)
	}

	if len(offenders) > 0 {
		t.Fatalf("found fenced code blocks outside examples/: %s", strings.Join(offenders, ", "))
	}
}
