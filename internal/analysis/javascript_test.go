package analysis_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"graphdb/internal/analysis"
)

func TestParseJavaScript(t *testing.T) {
	parser, ok := analysis.GetParser(".js")
	if !ok {
		t.Fatalf("JavaScript parser not registered")
	}

	absPath, err := filepath.Abs("../../test/fixtures/javascript/sample.js")
	if err != nil {
		t.Fatalf("Failed to get absolute path: %v", err)
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	nodes, edges, err := parser.Parse(absPath, content)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Helper to find edge
	hasEdge := func(srcName, tgtName string) bool {
		for _, e := range edges {
			srcMatch := strings.HasSuffix(e.SourceID, ":"+srcName+":") || strings.HasSuffix(e.SourceID, ":"+srcName)
			tgtMatch := strings.HasSuffix(e.TargetID, ":"+tgtName+":") || strings.HasSuffix(e.TargetID, ":"+tgtName)
			if srcMatch && tgtMatch {
				return true
			}
		}
		return false
	}

	// Basic checks
	foundHello := false
	foundGreeter := false

	for _, n := range nodes {
		name, _ := n.Properties["name"].(string)
		if name == "hello" && n.Label == "Function" {
			foundHello = true
			if _, ok := n.Properties["end_line"]; !ok {
				t.Errorf("Function 'hello' missing end_line")
			}
			startLine, _ := n.Properties["start_line"].(int)
			endLine, _ := n.Properties["end_line"].(int)
			if startLine == endLine {
				t.Errorf("Function 'hello' should span multiple lines, got start_line=%d end_line=%d", startLine, endLine)
			}
		}
		if name == "Greeter" && n.Label == "Class" {
			foundGreeter = true
			if _, ok := n.Properties["end_line"]; !ok {
				t.Errorf("Class 'Greeter' missing end_line")
			}
			startLine, _ := n.Properties["start_line"].(int)
			endLine, _ := n.Properties["end_line"].(int)
			if startLine == endLine {
				t.Errorf("Class 'Greeter' should span multiple lines, got start_line=%d end_line=%d", startLine, endLine)
			}
		}
	}

	if !foundHello {
		t.Errorf("Expected Function 'hello' not found")
	}
	if !foundGreeter {
		t.Errorf("Expected Class 'Greeter' not found")
	}

	if !hasEdge("main", "hello") {
		t.Errorf("Expected Call Edge main -> hello not found")
	}

	// Check for Import/Require Resolution
	foundUserUsage := false
	for _, e := range edges {
		srcMatch := strings.HasSuffix(e.SourceID, ":main:") || strings.HasSuffix(e.SourceID, ":main")
		if srcMatch && strings.Contains(e.TargetID, "models/User.js:User") {
			foundUserUsage = true
			break
		}
	}
	if !foundUserUsage {
		t.Errorf("Expected Call Edge main -> models/User.js:User not found")
	}

	// Check for Extends
	foundExtends := false
	for _, e := range edges {
		srcMatch := strings.HasSuffix(e.SourceID, ":SuperUser:") || strings.HasSuffix(e.SourceID, ":SuperUser")
		if srcMatch &&
			strings.Contains(e.TargetID, "models/User.js:User") &&
			e.Type == "EXTENDS" {
			foundExtends = true
			break
		}
	}
	if !foundExtends {
		t.Errorf("Expected EXTENDS Edge SuperUser -> models/User.js:User not found")
	}

	// Check for Fields
	foundRole := false
	for _, n := range nodes {
		name, _ := n.Properties["name"].(string)
		if name == "role" && n.Label == "Field" {
			foundRole = true
		}
	}
	if !foundRole {
		t.Errorf("Expected Field 'role' not found")
	}

	// Check that we don't have an edge to raw alias "UserAlias"
	foundAliasEdge := false
	for _, e := range edges {
		if strings.HasSuffix(e.TargetID, ":UserAlias") {
			foundAliasEdge = true
			break
		}
	}
	if foundAliasEdge {
		t.Errorf("Found edge to alias 'UserAlias', expected resolution to 'User'")
	}
}

func TestParseJavaScript_ClassAndConstructor(t *testing.T) {
	parser, ok := analysis.GetParser(".js")
	if !ok {
		t.Fatalf("JavaScript parser not registered")
	}

	absPath, err := filepath.Abs("dummy_collision.js")
	if err != nil {
		t.Fatalf("Failed to get absolute path: %v", err)
	}

	content := []byte(`
class User {
    constructor() { }
    save() { }
}

class Order {
    constructor() { }
    save() { }
}
`)

	nodes, _, err := parser.Parse(absPath, content)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	ids := make(map[string]int)
	for _, n := range nodes {
		ids[n.ID]++
	}

	for id, count := range ids {
		if count > 1 {
			t.Errorf("Duplicate ID found: %s (Count: %d)", id, count)
		}
	}

	expectedIDs := []string{
		"Class:" + absPath + ":User:",
		"Function:" + absPath + ":User.constructor:",
		"Function:" + absPath + ":User.save:",
		"Class:" + absPath + ":Order:",
		"Function:" + absPath + ":Order.constructor:",
		"Function:" + absPath + ":Order.save:",
	}
	for _, expected := range expectedIDs {
		if _, exists := ids[expected]; !exists {
			t.Errorf("Expected ID not found: %s", expected)
		}
	}
}
