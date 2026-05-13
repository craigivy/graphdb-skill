package main

import (
	"flag"
	"graphdb/internal/config"
	"graphdb/internal/rpg"
	"log"
	"os"
	"path/filepath"
)

func handleEnrichFeatures(args []string) {
	fs := flag.NewFlagSet("enrich-features", flag.ExitOnError)
	dirPtr := fs.String("dir", ".", "Directory to analyze")
	batchSizePtr := fs.Int("batch-size", 20, "Batch size for LLM feature extraction")
	cfg := config.LoadConfig()
	llmConcurrencyPtr := fs.Int("llm-concurrency", cfg.LLMConcurrency, "Number of concurrent LLM requests during extraction/summarization")
	embedBatchSizePtr := fs.Int("embed-batch-size", 100, "Batch size for embedding generation")
	seedPtr := fs.Int64("seed", 42, "Seed for deterministic K-Means clustering")
	appContextPtr := fs.String("app-context", "", "Optional path to an OVERVIEW.md or context preamble file")

	fs.Parse(args)

	if cfg.GoogleCloudLocation == "" {
		cfg.GoogleCloudLocation = "us-central1"
	}

	if cfg.GeminiEmbeddingModel == "" {
		cfg.GeminiEmbeddingModel = "gemini-embedding-001"
	}

	if cfg.GeminiGenerativeModel == "" {
		log.Fatal("GEMINI_GENERATIVE_MODEL is not set. Please set it in your .env file or environment.\n" +
			"Example: export GEMINI_GENERATIVE_MODEL=gemini-1.5-flash")
	}

	if cfg.GoogleCloudProject == "" {
		log.Fatal("GOOGLE_CLOUD_PROJECT is not set. Please set it in your .env file or environment.\n" +
			"Example: export GOOGLE_CLOUD_PROJECT=my-project-id")
	}

	if cfg.Neo4jURI == "" {
		log.Fatal("NEO4J_URI environment variable is not set")
	}

	// Load Application Context
	appContext := ""
	if *appContextPtr != "" {
		data, err := os.ReadFile(*appContextPtr)
		if err == nil {
			appContext = string(data)
		} else {
			log.Printf("Warning: Failed to read app-context file %s: %v", *appContextPtr, err)
		}
	} else {
		// Fallback to OVERVIEW.md in the target directory
		data, err := os.ReadFile(filepath.Join(*dirPtr, "OVERVIEW.md"))
		if err == nil {
			appContext = string(data)
		}
	}

	log.Println("Connecting to Graph Database...")
	provider, err := setupProvider(cfg)
	if err != nil {
		log.Fatalf("Failed to connect to Neo4j: %v", err)
	}
	defer provider.Close()

	extractor := setupExtractor(cfg, appContext)
	embedder := setupEmbedder(cfg)
	summarizer := setupSummarizer(cfg, appContext)

	orchestrator := &rpg.Orchestrator{
		Provider:       provider,
		Extractor:      extractor,
		Embedder:       embedder,
		Summarizer:     summarizer,
		Seed:           *seedPtr,
		LLMConcurrency: *llmConcurrencyPtr,
	}

	log.Println("Starting Database-backed Feature Enrichment...")

	// 1. Atomic Feature Extraction
	if err := orchestrator.RunExtraction(*batchSizePtr, *dirPtr); err != nil {
		log.Fatalf("Extraction failed: %v", err)
	}

	// 2. Embedding Generation
	if err := orchestrator.RunEmbedding(*embedBatchSizePtr); err != nil {
		log.Fatalf("Embedding generation failed: %v", err)
	}

	// 3. Out-of-Core Clustering (Topology generation)
	if err := orchestrator.RunClustering(*dirPtr); err != nil {
		log.Fatalf("Clustering failed: %v", err)
	}

	// 4. Summarization
	if err := orchestrator.RunSummarization(*batchSizePtr, *dirPtr); err != nil {
		log.Fatalf("Summarization failed: %v", err)
	}

	log.Println("Feature enrichment completed successfully.")
}
