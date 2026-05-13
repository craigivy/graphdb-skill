package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"graphdb/internal/config"
	"graphdb/internal/ingest"
	"graphdb/internal/loader"
	"graphdb/internal/storage"
	"graphdb/internal/ui"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

func handleIngest(args []string) {
	flags := flag.NewFlagSet("ingest", flag.ExitOnError)
	dirPtr := flags.String("dir", ".", "Directory to walk (ignored if -file-list is used)")
	fileListPtr := flags.String("file-list", "", "Path to a file containing a list of files to process")
	sinceCommitPtr := flags.String("since-commit", "", "Commit hash for incremental ingestion (skips JSONL, writes to DB)")
	workersPtr := flags.Int("workers", 4, "Number of workers")
	outputPtr := flags.String("output", "graph.jsonl", "Output file path (combined)")
	nodesPtr := flags.String("nodes", "", "Output file path for nodes")
	edgesPtr := flags.String("edges", "", "Output file path for edges")

	flags.Parse(args)

	cfg := config.LoadConfig()

	var emitter storage.Emitter
	var neoDriver neo4j.DriverWithContext
	var changedFiles []string

	sinceCommit := *sinceCommitPtr
	if sinceCommit == "" && *nodesPtr == "" && *edgesPtr == "" && *outputPtr == "graph.jsonl" && cfg.Neo4jURI != "" {
		// Auto-detect incremental mode
		provider, err := setupProvider(cfg)
		if err == nil {
			stateCommit, _ := provider.GetGraphState()
			if stateCommit != "" {
				cmd := exec.Command("git", "merge-base", "--is-ancestor", stateCommit, "HEAD")
				cmd.Dir = *dirPtr
				if err := cmd.Run(); err == nil {
					sinceCommit = stateCommit
					log.Printf("Auto-detected incremental mode from commit %s", sinceCommit)
				}
			}
			provider.Close()
		}
	}

	if sinceCommit != "" {
		log.Printf("Running in incremental mode since %s", sinceCommit)
		cmd := exec.Command("git", "diff", "--name-only", sinceCommit+"..HEAD")
		cmd.Dir = *dirPtr
		output, err := cmd.Output()
		if err != nil {
			log.Fatalf("Failed to get git diff: %v", err)
		}

		scanner := bufio.NewScanner(bytes.NewReader(output))
		for scanner.Scan() {
			if path := scanner.Text(); path != "" {
				changedFiles = append(changedFiles, path)
			}
		}

		if len(changedFiles) == 0 {
			log.Println("No supported files changed. Exiting.")
			return
		}

		log.Printf("Found %d changed files.", len(changedFiles))

		neoDriver, err = neo4j.NewDriverWithContext(cfg.Neo4jURI, neo4j.BasicAuth(cfg.Neo4jUser, cfg.Neo4jPassword, ""))
		if err != nil {
			log.Fatalf("Failed to create Neo4j driver: %v", err)
		}
		defer neoDriver.Close(context.Background())

		neo4jLoader := loader.NewNeo4jLoader(neoDriver, cfg.Neo4jDatabase, cfg.GeminiEmbeddingDimensions)
		emitter = storage.NewNeo4jEmitter(neo4jLoader, context.Background(), 500)

	} else if *nodesPtr != "" || *edgesPtr != "" {
		if *nodesPtr == "" || *edgesPtr == "" {
			log.Fatalf("Both -nodes and -edges must be provided for split output")
		}
		nodeFile, err := os.Create(*nodesPtr)
		if err != nil {
			log.Fatalf("Failed to create nodes file: %v", err)
		}
		edgeFile, err := os.Create(*edgesPtr)
		if err != nil {
			log.Fatalf("Failed to create edges file: %v", err)
		}
		emitter = storage.NewSplitJSONLEmitter(nodeFile, edgeFile)
	} else {
		// Setup Combined Emitter
		outFile, err := os.Create(*outputPtr)
		if err != nil {
			log.Fatalf("Failed to create output file: %v", err)
		}
		emitter = storage.NewJSONLEmitter(outFile)
	}
	defer emitter.Close()

	// Setup Walker
	walker := ingest.NewWalker(*workersPtr, emitter)

	// Count files first for progress bar
	var totalFiles int64
	if sinceCommit != "" {
		totalFiles = int64(len(changedFiles))
	} else if *fileListPtr != "" {
		log.Printf("Counting files in list %s...", *fileListPtr)
		file, err := os.Open(*fileListPtr)
		if err != nil {
			log.Fatalf("Failed to open file list: %v", err)
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			if scanner.Text() != "" {
				totalFiles++
			}
		}
		file.Close()
	} else {
		log.Printf("Counting files in %s...", *dirPtr)
		var err error
		totalFiles, err = walker.Count(context.Background(), *dirPtr)
		if err != nil {
			log.Fatalf("Failed to count files: %v", err)
		}
	}
	log.Printf("Found %d files to process", totalFiles)

	pb := ui.NewProgressBar(totalFiles, "Processing files")
	walker.WorkerPool.OnProgress = func() {
		pb.Add(1)
	}
	defer pb.Finish()

	// Context with Cancel
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle Signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Received shutdown signal...")
		cancel()
	}()

	// Run
	start := time.Now()

	if sinceCommit != "" {
		log.Printf("Starting incremental ingestion with %d workers...", *workersPtr)
		walker.WorkerPool.Start()
		for _, path := range changedFiles {
			walker.WorkerPool.Submit(*dirPtr, path)
		}
		walker.WorkerPool.Stop()
	} else if *fileListPtr != "" {
		log.Printf("Starting ingestion from file list %s with %d workers...", *fileListPtr, *workersPtr)
		file, err := os.Open(*fileListPtr)
		if err != nil {
			log.Fatalf("Failed to open file list: %v", err)
		}
		defer file.Close()

		walker.WorkerPool.Start()
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			path := scanner.Text()
			if path != "" {
				walker.WorkerPool.Submit(*dirPtr, path)
			}
		}
		walker.WorkerPool.Stop()
	} else {
		log.Printf("Starting walk on %s with %d workers...", *dirPtr, *workersPtr)
		if err := walker.Run(ctx, *dirPtr); err != nil {
			log.Fatalf("Walker failed: %v", err)
		}
	}

	log.Printf("Done in %v.", time.Since(start))
}
