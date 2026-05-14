//go:build !test_mocks

package main

import (
	"context"
	"graphdb/internal/config"
	"graphdb/internal/embedding"
	"graphdb/internal/loader"
	"graphdb/internal/query"
	"graphdb/internal/rpg"
	"log"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

func setupEmbedder(cfg config.Config) embedding.Embedder {
	ctx := context.Background()
	embedder, err := embedding.NewVertexEmbedder(ctx, cfg)
	if err != nil {
		log.Fatalf("Failed to initialize Vertex Embedder: %v", err)
	}
	return embedder
}

func setupSummarizer(cfg config.Config, appContext string) rpg.Summarizer {
	ctx := context.Background()
	summarizer, err := rpg.NewVertexSummarizer(ctx, cfg, appContext)
	if err != nil {
		log.Fatalf("Failed to initialize Vertex Summarizer: %v", err)
	}
	return summarizer
}

func setupExtractor(cfg config.Config, appContext string) rpg.FeatureExtractor {
	ctx := context.Background()
	extractor, err := rpg.NewLLMFeatureExtractor(ctx, cfg, appContext)
	if err != nil {
		log.Fatalf("Failed to initialize Vertex Feature Extractor: %v", err)
	}
	return extractor
}

func setupProvider(cfg config.Config) (query.GraphProvider, error) {
	return query.NewNeo4jProvider(cfg)
}

func setupDriver(cfg config.Config) (neo4j.DriverWithContext, error) {
	return neo4j.NewDriverWithContext(cfg.Neo4jURI, neo4j.BasicAuth(cfg.Neo4jUser, cfg.Neo4jPassword, ""))
}

func setupLoader(ctx context.Context, cfg config.Config, driver neo4j.DriverWithContext) (loader.Loader, error) {
	return loader.NewNeo4jLoader(driver, cfg.Neo4jDatabase, cfg.GeminiEmbeddingDimensions), nil
}
