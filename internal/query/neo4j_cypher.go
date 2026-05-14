package query

import (
	"fmt"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// RunCypher executes an arbitrary read-only Cypher query and returns the raw results.
func (p *Neo4jProvider) RunCypher(query string) ([]map[string]any, error) {
	// Execute via read transaction directly using session
	session := p.driver.NewSession(p.ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead, DatabaseName: p.dbName})
	defer session.Close(p.ctx)

	result, err := session.ExecuteRead(p.ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		res, err := tx.Run(p.ctx, query, nil)
		if err != nil {
			return nil, err
		}

		var records []map[string]any
		for res.Next(p.ctx) {
			record := res.Record()
			
			// Extract all values from the record as a map
			row := make(map[string]any)
			for _, key := range record.Keys {
				val, _ := record.Get(key)
				// We do some basic unwrapping for neo4j.Node if needed, 
				// but Neo4j driver types generally serialize to JSON reasonably well
				// or are simple native types for arbitrary projections.
				
				switch v := val.(type) {
				case neo4j.Node:
					props := v.GetProperties()
					props["_id"] = v.ElementId
					props["_labels"] = v.Labels
					row[key] = props
				case neo4j.Relationship:
					props := v.GetProperties()
					props["_id"] = v.ElementId
					props["_type"] = v.Type
					row[key] = props
				default:
					row[key] = v
				}
			}
			records = append(records, row)
		}
		
		if err = res.Err(); err != nil {
			return nil, err
		}
		return records, nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to run cypher: %w", err)
	}

	return result.([]map[string]any), nil
}
