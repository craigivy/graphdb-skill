package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"graphdb/internal/config"
	"graphdb/internal/query"
	"log"
	"os/exec"
	"sort"
	"strings"
)

func handleEnrichHistory(args []string) {
	fs := flag.NewFlagSet("enrich-history", flag.ExitOnError)
	dirPtr := fs.String("dir", "", "Directory to analyze (must be a git repository)")
	sincePtr := fs.String("since", "1 year ago", "How far back to analyze history")

	fs.Parse(args)

	cfg := config.LoadConfig()
	if *dirPtr != "" {
		cfg.BaseDir = *dirPtr
	}
	*dirPtr = cfg.BaseDir

	if cfg.Neo4jURI == "" {
		log.Fatal("NEO4J_URI environment variable is not set")
	}

	provider, err := setupProvider(cfg)
	if err != nil {
		log.Fatalf("Failed to connect to Neo4j: %v", err)
	}
	defer provider.Close()

	log.Printf("Analyzing git history in %s (since: %s)...", *dirPtr, *sincePtr)

	metrics, err := analyzeGitHistory(*dirPtr, *sincePtr)
	if err != nil {
		log.Fatalf("Failed to analyze git history: %v", err)
	}

	log.Printf("Found history for %d files. Updating graph...", len(metrics))
	
	if err := provider.UpdateFileHistory(metrics); err != nil {
		log.Fatalf("Failed to update file history in database: %v", err)
	}

	log.Println("Recalculating risk scores...")
	if err := provider.CalculateRiskScores(); err != nil {
		log.Fatalf("Failed to recalculate risk scores: %v", err)
	}

	log.Println("Git history enrichment completed successfully.")
}

func analyzeGitHistory(dir string, since string) (map[string]query.FileHistoryMetrics, error) {
	// Get commits and changed files
	// --name-only shows changed files
	// --format="COMMIT|%cI" helps delimit commits and get dates
	cmd := exec.Command("git", "log", "--since="+since, "--name-only", "--format=COMMIT|%cI")
	cmd.Dir = dir
	
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git log failed (is this a git repo?): %w", err)
	}

	return parseGitLog(output)
}

func parseGitLog(output []byte) (map[string]query.FileHistoryMetrics, error) {
	metrics := make(map[string]query.FileHistoryMetrics)
	scanner := bufio.NewScanner(bytes.NewReader(output))
	
	var currentCommitFiles []string
	var currentDate string
	
	// Map of File -> Map of Co-changed File -> Count
	coChangeCounts := make(map[string]map[string]int)

	processCommit := func() {
		if len(currentCommitFiles) == 0 {
			return
		}
		
		for i, file1 := range currentCommitFiles {
			// Update basic metrics
			m := metrics[file1]
			m.ChangeFrequency++
			if m.LastChanged == "" || currentDate > m.LastChanged {
				m.LastChanged = currentDate
			}
			metrics[file1] = m

			// Update co-change matrix
			if coChangeCounts[file1] == nil {
				coChangeCounts[file1] = make(map[string]int)
			}
			
			for j, file2 := range currentCommitFiles {
				if i != j {
					coChangeCounts[file1][file2]++
				}
			}
		}
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "COMMIT|") {
			processCommit()
			currentCommitFiles = nil // reset for new commit
			currentDate = strings.TrimPrefix(line, "COMMIT|")
		} else {
			// It's a file path
			currentCommitFiles = append(currentCommitFiles, line)
		}
	}
	// Process the final commit
	processCommit()

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Calculate top co-changes for each file
	for file, metric := range metrics {
		counts := coChangeCounts[file]
		
		type cc struct {
			file  string
			count int
		}
		var top []cc
		for f2, count := range counts {
			// Arbitrary threshold: must co-change at least 3 times to be interesting
			// (Can make this configurable later)
			if count >= 3 {
				top = append(top, cc{file: f2, count: count})
			}
		}
		
		sort.Slice(top, func(i, j int) bool {
			return top[i].count > top[j].count
		})
		
		var coChanges []string
		for i := 0; i < len(top) && i < 5; i++ { // Keep top 5
			coChanges = append(coChanges, top[i].file)
		}
		
		metric.CoChanges = coChanges
		metrics[file] = metric
	}

	return metrics, nil
}
