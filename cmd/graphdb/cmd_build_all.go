package main

import (
	"flag"
	"fmt"
	"graphdb/internal/config"
	"os"
)

func handleBuildAll(args []string) {
	fmt.Println("🚀 Starting GraphDB Build-All Sequence...")
	fmt.Println("========================================")

	fs := flag.NewFlagSet("build-all", flag.ExitOnError)
	dirPtr := fs.String("dir", "", "Directory to process")
	nodesPtr := fs.String("nodes", "nodes.jsonl", "Intermediate output file for nodes")
	edgesPtr := fs.String("edges", "edges.jsonl", "Intermediate output file for edges")
	fs.Parse(args)

	cfg := config.LoadConfig()
	if *dirPtr != "" {
		cfg.BaseDir = *dirPtr
	}
	*dirPtr = cfg.BaseDir

	// 1. Ingest
	fmt.Println("\n[Phase 1/6] Ingesting Codebase...")
	ingestArgs := []string{"-dir", *dirPtr, "-nodes", *nodesPtr, "-edges", *edgesPtr}
	ingestCmd(ingestArgs)

	// 2. Import Structural Graph
	fmt.Println("\n[Phase 2/6] Importing to Neo4j...")
	importArgs1 := []string{"-nodes", *nodesPtr, "-edges", *edgesPtr}
	importCmd(importArgs1)

	// 2.5 Cleanup intermediate files
	fmt.Println("\nCleaning up intermediate JSONL files...")
	if err := os.Remove(*nodesPtr); err != nil && !os.IsNotExist(err) {
		fmt.Printf("Warning: failed to remove %s: %v\n", *nodesPtr, err)
	}
	if err := os.Remove(*edgesPtr); err != nil && !os.IsNotExist(err) {
		fmt.Printf("Warning: failed to remove %s: %v\n", *edgesPtr, err)
	}

	// 3. Enrich Features
	fmt.Println("\n[Phase 3/6] Enriching Features (in-database)...")
	enrichArgs := []string{"-dir", *dirPtr}
	enrichCmd(enrichArgs)

	// 4. Enrich History
	fmt.Println("\n[Phase 4/6] Enriching Git History...")
	historyArgs := []string{"-dir", *dirPtr}
	enrichHistoryCmd(historyArgs)

	// 5. Enrich Contamination
	fmt.Println("\n[Phase 5/6] Enriching Contamination/Risk...")
	contaminationArgs := []string{}
	enrichContaminationCmd(contaminationArgs)

	// 6. Enrich Tests
	fmt.Println("\n[Phase 6/6] Linking Tests...")
	testArgs := []string{}
	enrichTestsCmd(testArgs)

	fmt.Println("\n✅ Build-All Sequence Complete!")
}
