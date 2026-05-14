package rpg

import (
	"fmt"
	"graphdb/internal/embedding"
	"graphdb/internal/graph"
	"graphdb/internal/query"
	"graphdb/internal/tools/snippet"
	"graphdb/internal/ui"
	"log"
	"math"
	"path/filepath"
	"sync"
)

type Orchestrator struct {
	Provider       query.GraphProvider
	Extractor      FeatureExtractor
	Embedder       embedding.Embedder
	Summarizer     Summarizer
	Seed           int64
	Loader         func(string, int, int) (string, error)
	LLMConcurrency int
}

func (o *Orchestrator) RunExtraction(batchSize int, dir string) error {
	log.Printf("Starting resumable extraction (batch size: %d)...", batchSize)

	total, err := o.Provider.CountUnextractedFunctions()
	if err != nil {
		return fmt.Errorf("failed to count unextracted functions: %w", err)
	}
	if total == 0 {
		log.Println("No unextracted functions to process")
		return nil
	}

	pb := ui.NewProgressBar(total, "Extracting features")
	defer pb.Finish()

	concurrency := o.LLMConcurrency
	if concurrency <= 0 {
		concurrency = 1
	}

	for {
		nodes, err := o.Provider.GetUnextractedFunctions(batchSize)
		if err != nil {
			return fmt.Errorf("failed to fetch unextracted functions: %w", err)
		}

		if len(nodes) == 0 {
			break
		}

		loader := o.Loader
		if loader == nil {
			loader = snippet.SliceFile
		}

		// Wrap loader to handle base directory
		wrappedLoader := func(path string, start, end int) (string, error) {
			fullPath := path
			if dir != "" && dir != "." {
				fullPath = filepath.Join(dir, path)
			}
			return loader(fullPath, start, end)
		}

		var wg sync.WaitGroup
		sem := make(chan struct{}, concurrency)
		errChan := make(chan error, len(nodes))

		for _, node := range nodes {
			wg.Add(1)
			sem <- struct{}{}

			go func(node *graph.Node) {
				defer wg.Done()
				defer func() { <-sem }()

				name, _ := node.Properties["name"].(string)
				file, _ := node.Properties["file"].(string)
				startLineRaw, _ := node.Properties["start_line"]
				endLineRaw, _ := node.Properties["end_line"]

				startLine := 0
				endLine := 0
				if v, ok := startLineRaw.(int64); ok {
					startLine = int(v)
				} else if v, ok := startLineRaw.(int); ok {
					startLine = v
				}
				if v, ok := endLineRaw.(int64); ok {
					endLine = int(v)
				} else if v, ok := endLineRaw.(int); ok {
					endLine = v
				}

				if file == "" || startLine == 0 || endLine == 0 {
					_ = o.Provider.UpdateAtomicFeatures(node.ID, []string{"unknown"}, false)
					pb.Add(1)
					return
				}

				code, err := wrappedLoader(file, startLine, endLine)
				if err != nil {
					log.Printf("Warning: failed to slice file %s:%d-%d: %v", file, startLine, endLine, err)
					_ = o.Provider.UpdateAtomicFeatures(node.ID, []string{"unreadable_source"}, false)
					pb.Add(1)
					return
				}

				descriptors, isVolatile, err := o.Extractor.Extract(code, name)
				if err != nil {
					errChan <- fmt.Errorf("extraction failed for %s: %w", node.ID, err)
					return
				}

				if len(descriptors) == 0 {
					descriptors = []string{"no_features_detected"}
				}

				err = o.Provider.UpdateAtomicFeatures(node.ID, descriptors, isVolatile)
				if err != nil {
					errChan <- fmt.Errorf("failed to update atomic features for %s: %w", node.ID, err)
					return
				}
				pb.Add(1)
			}(node)
		}

		wg.Wait()
		close(errChan)
		for err := range errChan {
			if err != nil {
				return err
			}
		}
	}

	log.Println("Finished extraction")
	return nil
}

func (o *Orchestrator) RunEmbedding(batchSize int) error {
	totalToProcess, err := o.Provider.CountUnembeddedNodes()
	if err != nil {
		return fmt.Errorf("failed to count unembedded nodes: %w", err)
	}

	if totalToProcess == 0 {
		log.Println("No unembedded nodes found.")
		return nil
	}

	pb := ui.NewProgressBar(totalToProcess, "Embedding nodes")

	for {
		nodes, err := o.Provider.GetUnembeddedNodes(batchSize)
		if err != nil {
			return fmt.Errorf("failed to fetch unembedded nodes: %w", err)
		}

		if len(nodes) == 0 {
			break
		}

		var batchTexts []string
		var batchIDs []string

		for _, node := range nodes {
			text := NodeToText(*node)
			batchTexts = append(batchTexts, text)
			batchIDs = append(batchIDs, node.ID)
		}

		embeddings, err := o.Embedder.EmbedBatch(batchTexts)
		if err != nil {
			log.Printf("Warning: failed to embed batch: %v", err)
			return fmt.Errorf("embedding batch failed: %w", err)
		}

		for i, emb := range embeddings {
			err = o.Provider.UpdateEmbeddings(batchIDs[i], emb)
			if err != nil {
				return fmt.Errorf("failed to update embedding for %s: %w", batchIDs[i], err)
			}
			pb.Add(1)
		}
	}

	pb.Finish()
	log.Printf("Finished embedding for %d nodes", totalToProcess)
	return nil
}
// CalculateDomainK determines the number of top-level domains based on unique file count.
func CalculateDomainK(fileCount int) int {
	if fileCount == 0 {
		return 0
	}
	k := int(math.Sqrt(float64(fileCount) / 5.0))
	if k < 5 {
		return 5
	}
	if k > 50 {
		return 50
	}
	return k
}

func (o *Orchestrator) RunClustering(dir string) error {
	log.Println("Clearing existing feature topology...")
	if err := o.Provider.ClearFeatureTopology(); err != nil {
		return fmt.Errorf("failed to clear topology: %w", err)
	}

	log.Println("Fetching embeddings for clustering...")
	embeddings, err := o.Provider.GetEmbeddingsOnly()
	if err != nil {
		return fmt.Errorf("failed to get embeddings: %w", err)
	}
	log.Printf("Loaded %d embeddings for clustering", len(embeddings))

	log.Println("Fetching function metadata for clustering...")
	metadataNodes, err := o.Provider.GetFunctionMetadata()
	if err != nil {
		return fmt.Errorf("failed to get function metadata: %w", err)
	}

	// Filter down to only nodes that have embeddings available
	var functions []graph.Node
	for _, n := range metadataNodes {
		if _, ok := embeddings[n.ID]; ok {
			functions = append(functions, *n)
		}
	}

	// Count unique files from function metadata
	uniqueFiles := make(map[string]struct{})
	for _, fn := range functions {
		if file, ok := fn.Properties["file"].(string); ok {
			uniqueFiles[file] = struct{}{}
		}
	}
	fileCount := len(uniqueFiles)
	log.Printf("Detected %d unique files across %d functions", fileCount, len(functions))

	clusterer := &EmbeddingClusterer{
		Embedder:              o.Embedder,
		PrecomputedEmbeddings: embeddings,
		Seed:                  o.Seed,
	}

	innerGlobalClusterer := &EmbeddingClusterer{
		Embedder:              o.Embedder,
		PrecomputedEmbeddings: embeddings,
		Seed:                  o.Seed,
		LogLabel:              "domain",
		KStrategy: func(n int) int {
			return CalculateDomainK(fileCount)
		},
	}

	globalClusterer := &GlobalEmbeddingClusterer{
		Inner:                 innerGlobalClusterer,
		Summarizer:            nil, // Naming handled in RunSummarization step (resumable)
		Loader:                snippet.SliceFile,
		PrecomputedEmbeddings: embeddings,
	}

	builder := &Builder{
		Clusterer:       clusterer,
		GlobalClusterer: globalClusterer,
		OnPhaseStart: func(phaseName string, total int) {
			log.Printf("Clustering phase: %s (%d domains)", phaseName, total)
		},
		OnStepStart: func(stepName string, index, total int) {
			log.Printf("  Clustering domain (%d/%d): %s...", index, total, stepName)
		},
		OnStepEnd: func(stepName string) {
			log.Printf("  Finished domain: %s", stepName)
		},
	}

	log.Printf("Starting clustering with %d functions...", len(functions))

	features, edges, err := builder.Build(dir, functions)
	if err != nil {
		return fmt.Errorf("clustering failed: %w", err)
	}

	// Flatten features
	nodes, allEdges := Flatten(features, edges)
	var nodePointers []*graph.Node
	for i := range nodes {
		nodePointers = append(nodePointers, &nodes[i])
	}
	var edgePointers []*graph.Edge
	for i := range allEdges {
		edgePointers = append(edgePointers, &allEdges[i])
	}

	log.Printf("Writing %d feature nodes and %d edges to database (batch size: 500)...", len(nodePointers), len(edgePointers))
	if err := o.Provider.UpdateFeatureTopology(nodePointers, edgePointers); err != nil {
		return fmt.Errorf("failed to write topology: %w", err)
	}

	log.Println("Finished clustering")
	return nil
}

func (o *Orchestrator) RunSummarization(batchSize int, dir string) error {
	log.Printf("Starting resumable summarization (batch size: %d)...", batchSize)

	total, err := o.Provider.CountUnnamedFeatures()
	if err != nil {
		return fmt.Errorf("failed to count unnamed features: %w", err)
	}
	if total == 0 {
		log.Println("No unnamed features to summarize")
		return nil
	}

	pb := ui.NewProgressBar(total, "Summarizing features")
	defer pb.Finish()

	concurrency := o.LLMConcurrency
	if concurrency <= 0 {
		concurrency = 1
	}

	for {
		nodes, err := o.Provider.GetUnnamedFeatures(batchSize)
		if err != nil {
			return fmt.Errorf("failed to fetch unnamed features: %w", err)
		}

		if len(nodes) == 0 {
			break
		}

		loader := o.Loader
		if loader == nil {
			loader = snippet.SliceFile
		}

		// Wrap loader to handle base directory
		wrappedLoader := func(path string, start, end int) (string, error) {
			fullPath := path
			if dir != "" && dir != "." {
				fullPath = filepath.Join(dir, path)
			}
			return loader(fullPath, start, end)
		}

		enricher := &Enricher{
			Client:   o.Summarizer,
			Embedder: o.Embedder,
			Loader:   wrappedLoader,
		}

		var wg sync.WaitGroup
		sem := make(chan struct{}, concurrency)
		errChan := make(chan error, len(nodes))

		for _, node := range nodes {
			wg.Add(1)
			sem <- struct{}{}

			go func(node *graph.Node) {
				defer wg.Done()
				defer func() { <-sem }()

				domain, err := o.Provider.ExploreDomain(node.ID)
				if err != nil {
					errChan <- fmt.Errorf("failed to explore domain for %s: %w", node.ID, err)
					return
				}

				var memberFuncs []graph.Node
				for _, fn := range domain.Functions {
					memberFuncs = append(memberFuncs, *fn)
				}

				f := &Feature{
					ID:   node.ID,
					Name: "",
				}

				err = enricher.Enrich(f, memberFuncs, node.Label)
				if err != nil {
					errChan <- fmt.Errorf("summarization failed for %s (%s): %w", node.ID, node.Label, err)
					return
				}

				err = o.Provider.UpdateFeatureSummary(node.ID, f.Name, f.Description)
				if err != nil {
					errChan <- fmt.Errorf("failed to update feature summary for %s: %w", node.ID, err)
					return
				}
				pb.Add(1)
			}(node)
		}

		wg.Wait()
		close(errChan)
		for err := range errChan {
			if err != nil {
				return err
			}
		}
	}

	log.Println("Finished summarization")
	return nil
}
