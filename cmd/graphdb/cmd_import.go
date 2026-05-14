package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"graphdb/internal/config"
	"graphdb/internal/graph"
	"graphdb/internal/loader"
	"graphdb/internal/ui"
	"log"
	"os"
	"path/filepath"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

func handleImport(args []string) {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	nodesPtr := fs.String("nodes", "", "Path to nodes JSONL file")
	edgesPtr := fs.String("edges", "", "Path to edges JSONL file")
	inputPtr := fs.String("input", "", "Path to combined JSONL file (nodes + edges)")
	dirPtr := fs.String("dir", "", "Base directory for source files (used to associate state)")
	batchSizePtr := fs.Int("batch-size", 500, "Batch size for insertion")

	fs.Parse(args)

	if *inputPtr == "" && (*nodesPtr == "" || *edgesPtr == "") {
		log.Fatal("Either -input or both -nodes and -edges must be provided")
	}

	cfg := config.LoadConfig()
	if cfg.Neo4jURI == "" {
		log.Fatal("NEO4J_URI environment variable is not set")
	}

	driver, err := neo4j.NewDriverWithContext(cfg.Neo4jURI, neo4j.BasicAuth(cfg.Neo4jUser, cfg.Neo4jPassword, ""))
	if err != nil {
		log.Fatalf("Failed to create Neo4j driver: %v", err)
	}
	defer driver.Close(context.Background())

	loader := loader.NewNeo4jLoader(driver, cfg.Neo4jDatabase, cfg.GeminiEmbeddingDimensions)

	ctx := context.Background()

	// 1. Apply Constraints
	log.Println("Applying schema constraints...")
	if err := loader.ApplyConstraints(ctx); err != nil {
		log.Printf("Warning: failed to apply constraints: %v", err)
	}

	// 3. Load Nodes
	var nodeFiles []string
	if *inputPtr != "" {
		nodeFiles = append(nodeFiles, *inputPtr)
	}
	if *nodesPtr != "" {
		nodeFiles = append(nodeFiles, *nodesPtr)
	}

	for _, path := range nodeFiles {
		log.Printf("Importing nodes from %s...", path)
		if err := processBatches(path, *batchSizePtr, func(batch []json.RawMessage) error {
			var nodes []graph.Node
			for _, raw := range batch {
				flat, err := decodeJSONRow(raw)
				if err != nil {
					continue
				}

				// Heuristic: Edges have "source"
				if _, ok := flat["source"]; ok {
					continue // It's an edge
				}

				id, _ := flat["id"].(string)
				label, _ := flat["type"].(string)

				if id == "" {
					continue
				}

				// Remove ID and Type from properties
				delete(flat, "id")
				delete(flat, "type")

				n := graph.Node{
					ID:         id,
					Label:      label,
					Properties: flat,
				}
				nodes = append(nodes, n)
			}
			return loader.BatchLoadNodes(ctx, nodes)
		}); err != nil {
			log.Fatalf("Failed to import nodes: %v", err)
		}
	}

	// 3. Load Edges
	var edgeFiles []string
	if *inputPtr != "" {
		edgeFiles = append(edgeFiles, *inputPtr)
	}
	if *edgesPtr != "" {
		edgeFiles = append(edgeFiles, *edgesPtr)
	}

	for _, path := range edgeFiles {
		log.Printf("Importing edges from %s...", path)
		if err := processBatches(path, *batchSizePtr, func(batch []json.RawMessage) error {
			var edges []graph.Edge
			for _, raw := range batch {
				flat, err := decodeJSONRow(raw)
				if err != nil {
					continue
				}

				// Heuristic: Edges have "source"
				if _, ok := flat["source"]; !ok {
					continue // It's a node
				}

				src, _ := flat["source"].(string)
				tgt, _ := flat["target"].(string)
				typ, _ := flat["type"].(string)

				if src == "" || tgt == "" {
					continue
				}

				e := graph.Edge{
					SourceID: src,
					TargetID: tgt,
					Type:     typ,
				}
				edges = append(edges, e)
			}
			return loader.BatchLoadEdges(ctx, edges)
		}); err != nil {
			log.Fatalf("Failed to import edges: %v", err)
		}
	}

	log.Println("Import complete.")

	// 5. Update Graph State (Commit Hash)
	// Try to get current git commit
	if commit, err := getGitCommit(); err == nil && commit != "" {
		log.Printf("Updating graph state with commit %s...", commit)
		if err := loader.UpdateGraphState(ctx, commit, *dirPtr); err != nil {
			log.Printf("Warning: failed to update graph state: %v", err)
		}
	}
}

func processBatches(path string, batchSize int, process func([]json.RawMessage) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var scanner *bufio.Scanner
	if fi, err := f.Stat(); err == nil {
		pb := ui.NewProgressBar(fi.Size(), fmt.Sprintf("Importing %s", filepath.Base(path)))
		pb.SetFormat(ui.FormatBytesFn)
		defer pb.Finish()
		reader := &ui.ByteReader{Reader: f, Pb: pb}
		scanner = bufio.NewScanner(reader)
	} else {
		scanner = bufio.NewScanner(f)
	}

	// Increase buffer size for large lines
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 10*1024*1024)

	var batch []json.RawMessage
	for scanner.Scan() {
		line := scanner.Bytes()
		// Copy slice because scanner reuses it
		item := make([]byte, len(line))
		copy(item, line)

		batch = append(batch, item)

		if len(batch) >= batchSize {
			if err := process(batch); err != nil {
				return err
			}
			batch = batch[:0]
		}
	}

	if len(batch) > 0 {
		if err := process(batch); err != nil {
			return err
		}
	}

	return scanner.Err()
}

func convertNumbers(v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		for k, child := range val {
			val[k] = convertNumbers(child)
		}
		return val
	case []interface{}:
		for i, child := range val {
			val[i] = convertNumbers(child)
		}
		return val
	case json.Number:
		if i, err := val.Int64(); err == nil {
			return i
		}
		if f, err := val.Float64(); err == nil {
			return f
		}
		return val
	default:
		return val
	}
}

func decodeJSONRow(raw []byte) (map[string]interface{}, error) {
	var flat map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&flat); err != nil {
		return nil, err
	}

	for k, v := range flat {
		flat[k] = convertNumbers(v)
	}
	return flat, nil
}
