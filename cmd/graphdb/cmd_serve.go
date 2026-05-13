package main

import (
	"flag"
	"fmt"
	"graphdb/internal/config"
	"graphdb/internal/ui"
	"log"
	"net/http"
	"os"
)

func handleServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	portPtr := fs.Int("port", 8080, "Port to run the HTTP server on")
	locationPtr := fs.String("location", "global", "GCP Location")
	modelPtr := fs.String("model", "", "Embedding model name")

	fs.Parse(args)

	cfg := config.LoadConfig()
	if *modelPtr != "" {
		cfg.GeminiEmbeddingModel = *modelPtr
	}
	if *locationPtr != "" {
		cfg.GoogleCloudLocation = *locationPtr
	}

	if cfg.Neo4jURI == "" {
		fmt.Fprintf(os.Stderr, "Error: NEO4J_URI environment variable is not set\n")
		os.Exit(1)
	}

	provider, err := setupProvider(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to connect to Neo4j: %v\n", err)
		os.Exit(1)
	}

	embedder := setupEmbedder(cfg)

	server := ui.NewServer(provider, embedder, cfg, Version)

	addr := fmt.Sprintf(":%d", *portPtr)
	fmt.Printf("Starting GraphDB visualizer at http://localhost:%d\n", *portPtr)
	log.Printf("Starting web visualizer on http://localhost%s\n", addr)
	
	if err := http.ListenAndServe(addr, server); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Server failed to start on port %d: %v\n", *portPtr, err)
		log.Fatalf("Server failed: %v", err)
	}
}
