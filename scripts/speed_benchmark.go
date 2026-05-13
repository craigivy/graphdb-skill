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
	fmt.Println("=== LLM Concurrency Speed Test (Concurrency: 5) ===\n")

	concurrency := 5

	// Test 1: Cloud Run
	cloudRunCfg := config.Config{
		GoogleCloudProject:    "jasondel-cloudrun10",
		GoogleCloudLocation:   "us-central1",
		GeminiGenerativeModel: "gemma-4",
		GenAIBaseURL:          "https://gemma-litert-kk35opvuza-uc.a.run.app",
		GenAIAPIVersion:       "v1",
	}
	runTest("Custom Cloud Run (gemma-4)", cloudRunCfg, concurrency)

	fmt.Println("\n--------------------------------------------------\n")

	// Test 2: Vertex AI
	vertexCfg := config.Config{
		GoogleCloudProject:    "jasondel-cloudrun10",
		GoogleCloudLocation:   "global",
		GeminiGenerativeModel: "gemini-3.1-flash-lite",
	}
	runTest("Vertex AI Global (gemini-3.1-flash-lite)", vertexCfg, concurrency)
}

func runTest(name string, cfg config.Config, concurrency int) {
	fmt.Printf("Starting test: %s\n", name)
	ctx := context.Background()
	extractor, err := rpg.NewLLMFeatureExtractor(ctx, cfg, "Speed Test Context")
	if err != nil {
		log.Fatalf("Failed to initialize extractor: %v", err)
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)
	
	startTime := time.Now()

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		sem <- struct{}{}

		go func(id int) {
			defer wg.Done()
			defer func() { <-sem }()

			code := fmt.Sprintf(`
				function processPayment_%d(amount) {
					if (amount <= 0) throw new Error("Invalid");
					console.log("Processing payment for amount: " + amount);
					return { status: "success", amount: amount };
				}
			`, id)

			reqStart := time.Now()
			_, _, err := extractor.Extract(code, fmt.Sprintf("processPayment_%d", id))
			reqDuration := time.Since(reqStart)

			if err != nil {
				fmt.Printf("  [Req %d] ❌ FAILED in %v: %v\n", id, reqDuration, err)
			} else {
				fmt.Printf("  [Req %d] ✅ SUCCESS in %v\n", id, reqDuration)
			}
		}(i)
	}

	wg.Wait()
	totalDuration := time.Since(startTime)
	fmt.Printf("\n🏁 [%s]\n   Total Time for %d requests: %v\n", name, concurrency, totalDuration)
	
	reqPerSec := float64(concurrency) / totalDuration.Seconds()
	fmt.Printf("   Throughput: %.2f requests/second\n", reqPerSec)
}
