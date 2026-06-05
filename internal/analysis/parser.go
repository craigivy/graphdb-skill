package analysis

import (
	"fmt"
	"graphdb/internal/graph"
	"strings"
)

// LanguageParser defines the interface for parsing source code files.
type LanguageParser interface {
	Parse(filePath string, content []byte) ([]*graph.Node, []*graph.Edge, error)
}

var parsers = make(map[string]LanguageParser)

// RegisterParser registers a parser for a specific file extension (e.g., ".go").
func RegisterParser(ext string, p LanguageParser) {
	parsers[ext] = p
}

// GetParser retrieves the parser for the given extension.
func GetParser(ext string) (LanguageParser, bool) {
	p, ok := parsers[ext]
	return p, ok
}

// GenerateNodeID creates a deterministic unique ID for a code element
// using the format "Label:FQN:Signature". This prevents cross-label
// collisions (e.g. Class vs Constructor with same FQN) and overload
// collisions (e.g. methods with same name but different parameters).
func GenerateNodeID(label string, fqn string, signature string) string {
	return fmt.Sprintf("%s:%s:%s", label, fqn, signature)
}

// IsTestFile detects if a file path belongs to a test file by convention.
func IsTestFile(path string) bool {
	lowerPath := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lowerPath, "_test.go"):
		return true
	case strings.HasSuffix(lowerPath, "test.java"):
		return true
	case strings.HasSuffix(lowerPath, "tests.cs") || strings.HasSuffix(lowerPath, "test.cs"):
		return true
	case strings.Contains(lowerPath, "/tests/"):
		return true
	case strings.HasSuffix(lowerPath, ".test.ts"):
		return true
	case strings.HasSuffix(lowerPath, ".spec.ts"):
		return true
	case strings.HasSuffix(lowerPath, ".test.js"):
		return true
	case strings.HasSuffix(lowerPath, ".spec.js"):
		return true
	default:
		return false
	}
}
