package caddyslb

import (
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestDocs_LocalMarkdownLinksResolve(t *testing.T) {
	skipDirs := map[string]struct{}{
		".git": {},
	}

	// Inline: [text](target) and images: ![alt](target)
	// Note: this is intentionally conservative; we only validate filesystem targets.
	inlineLinkRe := regexp.MustCompile(`!?(?:\[[^\]]*\])\(([^)]+)\)`)
	// Reference-style link definitions: [id]: target
	refDefRe := regexp.MustCompile(`(?m)^\[[^\]]+\]:\s*(\S+)`)

	type offender struct {
		source string
		target string
	}

	var offenders []offender

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

		content := string(b)
		var targets []string

		for _, m := range inlineLinkRe.FindAllStringSubmatch(content, -1) {
			if len(m) < 2 {
				continue
			}
			targets = append(targets, m[1])
		}
		for _, m := range refDefRe.FindAllStringSubmatch(content, -1) {
			if len(m) < 2 {
				continue
			}
			targets = append(targets, m[1])
		}

		baseDir := filepath.Dir(path)
		for _, rawTarget := range targets {
			target := strings.TrimSpace(rawTarget)
			target = strings.Trim(target, "<>")
			target = strings.Trim(target, `"'`)

			if target == "" {
				continue
			}

			// Ignore pure anchors.
			if strings.HasPrefix(target, "#") {
				continue
			}

			// Ignore links with a URL scheme.
			if u, parseErr := url.Parse(target); parseErr == nil && u.Scheme != "" {
				continue
			}

			// Strip fragment/query.
			if i := strings.IndexByte(target, '#'); i >= 0 {
				target = target[:i]
			}
			if i := strings.IndexByte(target, '?'); i >= 0 {
				target = target[:i]
			}

			target = strings.TrimSpace(target)
			if target == "" {
				continue
			}

			// URL-decode path segments (e.g. %20).
			if decoded, decodeErr := url.PathUnescape(target); decodeErr == nil {
				target = decoded
			}

			// Resolve filesystem path.
			var resolved string
			if strings.HasPrefix(target, "/") {
				// Treat as repo-root absolute.
				resolved = filepath.Clean("." + target)
			} else {
				resolved = filepath.Clean(filepath.Join(baseDir, target))
			}

			if _, statErr := os.Stat(resolved); statErr != nil {
				offenders = append(offenders, offender{source: path, target: rawTarget})
			}
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walking markdown files: %v", err)
	}

	if len(offenders) > 0 {
		sort.Slice(offenders, func(i, j int) bool {
			if offenders[i].source == offenders[j].source {
				return offenders[i].target < offenders[j].target
			}
			return offenders[i].source < offenders[j].source
		})

		var b strings.Builder
		b.WriteString("broken local markdown links found:\n")
		for _, o := range offenders {
			b.WriteString("- ")
			b.WriteString(o.source)
			b.WriteString(": ")
			b.WriteString(o.target)
			b.WriteString("\n")
		}
		t.Fatal(b.String())
	}
}
