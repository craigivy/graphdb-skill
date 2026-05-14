package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	cliPathOnce sync.Once
	cliPath     string
)

func getRepoRoot(t *testing.T) string {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// Traverse up until we find go.mod
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			t.Fatal("Could not find repo root (go.mod)")
		}
		wd = parent
	}
}

func buildCLI(t *testing.T) string {
	cliPathOnce.Do(func() {
		root := getRepoRoot(t)
		outputPath := filepath.Join(root, "bin", "graphdb_test")
		cmdPath := filepath.Join(root, "cmd", "graphdb")

		// Ensure bin directory exists
		os.MkdirAll(filepath.Join(root, "bin"), 0755)

		cmd := exec.Command("go", "build", "-tags", "test_mocks", "-o", outputPath, cmdPath)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Failed to build CLI: %v\nOutput: %s", err, output)
		}
		cliPath = outputPath
	})
	return cliPath
}

func TestCLI_Ingest(t *testing.T) {
        cli := buildCLI(t)
        root := getRepoRoot(t)

        nodesFile := filepath.Join(root, "test_nodes.jsonl")
        edgesFile := filepath.Join(root, "test_edges.jsonl")
        defer os.Remove(nodesFile)
        defer os.Remove(edgesFile)

        fixturesPath := filepath.Join(root, "test", "fixtures", "typescript")

        // Run ingest
        cmd := exec.Command(cli, "ingest",
                "-dir", fixturesPath,
                "-nodes", nodesFile,
                "-edges", edgesFile,
        )
            cmd.Env = append(os.Environ(), "GRAPHDB_MOCK_ENABLED=true", "NEO4J_URI=bolt://mock", "NEO4J_USER=mock", "NEO4J_PASSWORD=mock")
            output, err := cmd.CombinedOutput()
            if err != nil {
                t.Fatalf("Ingest command failed: %v\nOutput: %s", err, output)
        }

        // Verify output file exists and has content
        nodeContent, err := os.ReadFile(nodesFile)
        if err != nil {
                t.Fatalf("Failed to read nodes file: %v", err)
        }
        if len(nodeContent) == 0 {
                t.Error("Nodes file is empty")
        }

        edgeContent, err := os.ReadFile(edgesFile)
        if err != nil {
                t.Fatalf("Failed to read edges file: %v", err)
        }
        if len(edgeContent) == 0 {
                t.Error("Edges file is empty")
        }
}
func TestCLI_Query_Help(t *testing.T) {
	cli := buildCLI(t)

	// Test help/unknown command
	cmd := exec.Command(cli, "unknown")
	output, err := cmd.CombinedOutput()

	// It should exit with 1
	if err == nil {
		t.Error("Expected error for unknown command, got nil")
	}
	if !strings.Contains(string(output), "Unknown command") {
		t.Errorf("Expected 'Unknown command' message, got: %s", output)
	}
}

func TestCLI_Query_Seams(t *testing.T) {
	    cli := buildCLI(t)
	
	    // Run query
	    cmd := exec.Command(cli, "query", "-type", "seams", "-module", ".*")
	    cmd.Env = append(os.Environ(), "GRAPHDB_MOCK_ENABLED=true", "NEO4J_URI=bolt://mock", "NEO4J_USER=mock", "NEO4J_PASSWORD=mock")
        output, err := cmd.Output()
        if err != nil {
		t.Fatalf("Query command failed: %v\nOutput: %s", err, output)
	}

	// Check for JSON output (starts with [ or is null or empty list)
	outStr := strings.TrimSpace(string(output))
	if !strings.HasPrefix(outStr, "[") && outStr != "null" {
		t.Errorf("Expected JSON array output, got: %s", outStr)
	}
}

func TestCLI_GlobalLogFile(t *testing.T) {
	cli := buildCLI(t)
	root := getRepoRoot(t)

	logFilePath := filepath.Join(root, "test_global.log")
	defer os.Remove(logFilePath)

	// Run an ingest command that will quickly fail or complete, but we pass the new global flag
	cmd := exec.Command(cli, "--log="+logFilePath, "ingest", "-dir", "nonexistent_dir_for_test")
	cmd.Env = append(os.Environ(), "GRAPHDB_MOCK_ENABLED=true", "NEO4J_URI=bolt://mock", "NEO4J_USER=mock", "NEO4J_PASSWORD=mock")
	_ = cmd.Run() // It's okay if it fails (due to nonexistent dir), we just care that it logs

	// Verify log file exists
	content, err := os.ReadFile(logFilePath)
	if err != nil {
		t.Fatalf("Log file was not created: %v", err)
	}

	// Verify log content has standard log formatting (timestamp) and contains an expected message
	outStr := string(content)
	if !strings.Contains(outStr, "nonexistent_dir_for_test") && !strings.Contains(outStr, "Found") && !strings.Contains(outStr, "failed") {
		t.Errorf("Log file missing expected content. Got: %s", outStr)
	}

	// Check that we see shortfile info (main.go or cmd_ingest.go)
	if !strings.Contains(outStr, ".go:") {
		t.Errorf("Log file missing file/line numbers. Got: %s", outStr)
	}
}


