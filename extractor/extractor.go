// Package extractor provides key extraction from HTTP requests using Caddy placeholder syntax.
package extractor

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/caddyserver/caddy/v2"
)

// KeyExtractor extracts routing keys from HTTP requests.
type KeyExtractor interface {
	// Extract returns the routing key from the request.
	// Returns empty string if key cannot be extracted.
	Extract(r *http.Request, repl *caddy.Replacer) string
}

// PlaceholderExtractor extracts a key using Caddy placeholder syntax.
type PlaceholderExtractor struct {
	// Expression is the placeholder expression, e.g., "{http.request.header.X-Tenant}"
	Expression string

	// parts holds parsed expression parts for composite expressions
	parts []expressionPart
}

type expressionPart struct {
	isPlaceholder bool
	value         string // literal value or placeholder expression
}

// placeholderRegex matches Caddy-style placeholders like {http.request.header.X-Tenant}
var placeholderRegex = regexp.MustCompile(`\{[^}]+\}`)

// NewFromExpression creates a KeyExtractor from a placeholder expression.
// Supports single placeholders like "{http.request.header.X-Tenant}"
// and composite expressions like "{header.X-Org}-{cookie.region}".
func NewFromExpression(expr string) (KeyExtractor, error) {
	if expr == "" {
		return nil, nil
	}

	// Expand shortcut placeholders to full form
	expanded := expandShortcuts(expr)

	extractor := &PlaceholderExtractor{
		Expression: expanded,
	}

	// Parse expression into parts
	extractor.parts = parseExpression(expanded)

	return extractor, nil
}

// expandShortcuts expands shortcut placeholders to their full form.
// e.g., {header.X-Tenant} -> {http.request.header.X-Tenant}
func expandShortcuts(expr string) string {
	shortcuts := map[string]string{
		"{header.": "{http.request.header.",
		"{cookie.": "{http.request.cookie.",
		"{query.":  "{http.request.uri.query.",
		"{path.":   "{http.request.uri.path.",
		"{path}":   "{http.request.uri.path}",
		"{host}":   "{http.request.host}",
		"{method}": "{http.request.method}",
		"{remote}": "{http.request.remote}",
		"{scheme}": "{http.request.scheme}",
	}

	result := expr
	for shortcut, full := range shortcuts {
		result = strings.ReplaceAll(result, shortcut, full)
	}
	return result
}

// parseExpression parses an expression into literal and placeholder parts.
func parseExpression(expr string) []expressionPart {
	lastEnd := 0
	matches := placeholderRegex.FindAllStringIndex(expr, -1)
	parts := make([]expressionPart, 0, len(matches)*2+1)

	for _, match := range matches {
		// Add literal part before this placeholder
		if match[0] > lastEnd {
			parts = append(parts, expressionPart{
				isPlaceholder: false,
				value:         expr[lastEnd:match[0]],
			})
		}

		// Add placeholder part
		parts = append(parts, expressionPart{
			isPlaceholder: true,
			value:         expr[match[0]:match[1]],
		})

		lastEnd = match[1]
	}

	// Add remaining literal part
	if lastEnd < len(expr) {
		parts = append(parts, expressionPart{
			isPlaceholder: false,
			value:         expr[lastEnd:],
		})
	}

	return parts
}

// Extract implements KeyExtractor.
func (e *PlaceholderExtractor) Extract(r *http.Request, repl *caddy.Replacer) string {
	if repl == nil || len(e.parts) == 0 {
		return ""
	}

	var result strings.Builder

	for _, part := range e.parts {
		if part.isPlaceholder {
			// Use Caddy's replacer to resolve the placeholder
			// Remove the curly braces for the replacer
			key := strings.Trim(part.value, "{}")
			val, ok := repl.GetString(key)
			if !ok || val == "" {
				// If any placeholder resolves to empty, return empty
				return ""
			}
			result.WriteString(val)
		} else {
			result.WriteString(part.value)
		}
	}

	return result.String()
}

// String returns the original expression.
func (e *PlaceholderExtractor) String() string {
	return e.Expression
}
