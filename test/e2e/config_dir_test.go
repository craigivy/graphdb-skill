package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLI_GraphdbDirEnv(t *testing.T) {
	cli := buildCLI(t)
	root := getRepoRoot(t)

	nodesFile := filepath.Join(root, "test_env_nodes.jsonl")
	edgesFile := filepath.Join(root, "test_env_edges.jsonl")
	defer os.Remove(nodesFile)
	defer os.Remove(edgesFile)

	fixturesPath := filepath.Join(root, "test", "fixtures", "typescript")

	// Run ingest WITHOUT -dir flag, but WITH GRAPHDB_DIR env var
	cmd := exec.Command(cli, "ingest",
		"-nodes", nodesFile,
		"-edges", edgesFile,
	)
	cmd.Env = append(os.Environ(), 
		"GRAPHDB_MOCK_ENABLED=true", 
		"NEO4J_URI=bolt://mock", 
		"NEO4J_USER=mock", 
		"NEO4J_PASSWORD=mock",
		"GRAPHDB_DIR="+fixturesPath,
	)
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Ingest command failed with GRAPHDB_DIR: %v\nOutput: %s", err, output)
	}

	// Verify output files exist and have content
	if _, err := os.Stat(nodesFile); err != nil {
		t.Errorf("Nodes file not created with GRAPHDB_DIR: %v", err)
	}
	if _, err := os.Stat(edgesFile); err != nil {
		t.Errorf("Edges file not created with GRAPHDB_DIR: %v", err)
	}
}

func TestCLI_DirFlagOverridesEnv(t *testing.T) {
	cli := buildCLI(t)
	root := getRepoRoot(t)

	nodesFile := filepath.Join(root, "test_override_nodes.jsonl")
	edgesFile := filepath.Join(root, "test_override_edges.jsonl")
	defer os.Remove(nodesFile)
	defer os.Remove(edgesFile)

	// Valid fixtures path
	fixturesPath := filepath.Join(root, "test", "fixtures", "typescript")
	// Invalid path for env var
	invalidPath := filepath.Join(root, "nonexistent_dir_for_override_test")

	// Run ingest WITH -dir flag AND GRAPHDB_DIR env var.
	// Flag should win.
	cmd := exec.Command(cli, "ingest",
		"-dir", fixturesPath,
		"-nodes", nodesFile,
		"-edges", edgesFile,
	)
	cmd.Env = append(os.Environ(), 
		"GRAPHDB_MOCK_ENABLED=true", 
		"NEO4J_URI=bolt://mock", 
		"NEO4J_USER=mock", 
		"NEO4J_PASSWORD=mock",
		"GRAPHDB_DIR="+invalidPath,
	)
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Ingest command failed with -dir flag override: %v\nOutput: %s", err, output)
	}

	// Verify output files exist
	if _, err := os.Stat(nodesFile); err != nil {
		t.Errorf("Nodes file not created with -dir flag override: %v", err)
	}
}

func TestCLI_Query_GraphdbDirEnv(t *testing.T) {
	cli := buildCLI(t)
	tmpDir := t.TempDir()

	// Run status query without -dir flag, but with GRAPHDB_DIR
	cmd := exec.Command(cli, "query", "-type", "status")
	cmd.Env = append(os.Environ(), 
		"GRAPHDB_MOCK_ENABLED=true", 
		"NEO4J_URI=bolt://mock", 
		"NEO4J_USER=mock", 
		"NEO4J_PASSWORD=mock",
		"GRAPHDB_DIR="+tmpDir,
	)
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Query status failed with GRAPHDB_DIR: %v\nOutput: %s", err, output)
	}

	// Should return valid JSON with commit (mock returns empty string by default in mocks.go)
	if !strings.Contains(string(output), "\"commit\"") {
		t.Errorf("Expected status JSON output, got: %s", output)
	}
}

func TestCLI_Import_GraphdbDirEnv(t *testing.T) {
	cli := buildCLI(t)
	root := getRepoRoot(t)
	tmpDir := t.TempDir()

	// Create a dummy nodes file
	nodesFile := filepath.Join(root, "test_import_nodes.jsonl")
	os.WriteFile(nodesFile, []byte("{\"id\":\"1\",\"type\":\"Function\"}\n"), 0644)
	defer os.Remove(nodesFile)

	// Run import without -dir flag, but with GRAPHDB_DIR
	cmd := exec.Command(cli, "import", "-nodes", nodesFile, "-edges", nodesFile) // reuse file for edges, it will be ignored by logic
	cmd.Env = append(os.Environ(), 
		"GRAPHDB_MOCK_ENABLED=true", 
		"NEO4J_URI=bolt://mock", 
		"NEO4J_USER=mock", 
		"NEO4J_PASSWORD=mock",
		"GRAPHDB_DIR="+tmpDir,
	)
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Import might fail if it tries to actually connect to Neo4j even in mock mode if setup is incomplete
		// but mocks.go should handle it.
		t.Fatalf("Import failed with GRAPHDB_DIR: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(string(output), "Import complete") {
		t.Errorf("Expected 'Import complete' message, got: %s", output)
	}
}
