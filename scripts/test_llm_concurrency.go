//go:build ignore

package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"graphdb/internal/config"
	"graphdb/internal/rpg"
)

func main() {
	fmt.Println("=== Testing Generative LLM Concurrency ===")
	
	// Test 1: Cloud Run Endpoint (gemma-4)
	fmt.Println("\n--- Test 1: Gemma-4 (Cloud Run Endpoint) ---")
	testEndpoint(config.Config{
		GoogleCloudProject:    "jasondel-cloudrun10",
		GoogleCloudLocation:   "us-central1", // Or any region, BaseURL ignores it
		GeminiGenerativeModel: "gemma-4",
		GenAIBaseURL:          "https://gemma-litert-kk35opvuza-uc.a.run.app",
		GenAIAPIVersion:       "v1", // Default Vertex API version
	})

	// Test 2: Vertex AI Endpoint (gemini-3.1-flash-lite)
	fmt.Println("\n--- Test 2: Gemini 3.1 Flash Lite (Vertex AI Global Region) ---")
	testEndpoint(config.Config{
		GoogleCloudProject:    "jasondel-cloudrun10",
		GoogleCloudLocation:   "global",
		GeminiGenerativeModel: "gemini-3.1-flash-lite",
		// Note: GenAIBaseURL is empty, so it uses the native Vertex endpoint
	})
}

func testEndpoint(cfg config.Config) {
	ctx := context.Background()
	extractor, err := rpg.NewLLMFeatureExtractor(ctx, cfg, "Test Application Context")
	if err != nil {
		log.Fatalf("Failed to initialize extractor: %v", err)
	}

	concurrency := 5
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)

	fmt.Printf("Dispatching %d concurrent requests to %s...\n", concurrency, cfg.GeminiGenerativeModel)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		sem <- struct{}{} // Acquire semaphore

		go func(id int) {
			defer wg.Done()
			defer func() { <-sem }() // Release semaphore

			code := fmt.Sprintf(`
				function processPayment_%d(amount) {
					console.log("Processing payment for amount: " + amount);
					return true;
				}
			`, id)

			start := time.Now()
			descriptors, isVolatile, err := extractor.Extract(code, fmt.Sprintf("processPayment_%d", id))
			duration := time.Since(start)

			if err != nil {
				fmt.Printf(" [Req %d] FAILED in %v: %v\n", id, duration, err)
			} else {
				fmt.Printf(" [Req %d] SUCCESS in %v - Volatile: %v, Descriptors: %v\n", id, duration, isVolatile, descriptors)
			}
		}(i)
	}

	wg.Wait()
	fmt.Println("All concurrent requests completed.")
}
