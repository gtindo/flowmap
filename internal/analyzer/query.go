package analyzer

import (
	"fmt"
	"sort"
	"strings"
)

// Search finds named functions using case-insensitive qualified-name matching.
func (index *Index) Search(query string, includeTests bool, limit int) []SearchResult {
	if limit <= 0 {
		limit = 50
	}
	normalizedQuery := strings.ToLower(strings.TrimSpace(query))
	results := make([]SearchResult, 0)
	for _, function := range index.Functions {
		if function.Test && !includeTests {
			continue
		}
		if normalizedQuery != "" && !strings.Contains(strings.ToLower(function.QualifiedName), normalizedQuery) && !strings.Contains(strings.ToLower(function.Package), normalizedQuery) {
			continue
		}
		results = append(results, SearchResult{
			ID: function.ID, QualifiedName: function.QualifiedName, Package: function.Package,
			Signature: function.Signature, Classification: function.Classification.Kind, Test: function.Test,
		})
	}
	sort.Slice(results, func(left, right int) bool {
		return results[left].QualifiedName < results[right].QualifiedName
	})
	if len(results) > limit {
		return results[:limit]
	}
	return results
}

// Function returns one indexed function by stable ID.
func (index *Index) Function(id string) (Function, bool) {
	function, ok := index.Functions[id]
	return function, ok
}

// Focus returns the bounded upstream, downstream, or bidirectional neighborhood.
func (index *Index) Focus(rootID string, direction string, depth int, includeTests bool) (Graph, error) {
	root, exists := index.Functions[rootID]
	if !exists {
		return Graph{}, fmt.Errorf("unknown function %q", rootID)
	}
	if depth < 0 {
		depth = 0
	}
	if depth > 8 {
		depth = 8
	}
	if direction != "upstream" && direction != "downstream" && direction != "both" {
		direction = "downstream"
	}

	selected := map[string]bool{rootID: true}
	frontier := []string{rootID}
	for level := 0; level < depth; level++ {
		next := make([]string, 0)
		for _, id := range frontier {
			candidateEdges := make([]Edge, 0)
			if direction == "downstream" || direction == "both" {
				candidateEdges = append(candidateEdges, index.Outgoing[id]...)
			}
			if direction == "upstream" || direction == "both" {
				candidateEdges = append(candidateEdges, index.Incoming[id]...)
			}
			for _, edge := range candidateEdges {
				neighbor := edge.CalleeID
				if neighbor == id {
					neighbor = edge.CallerID
				} else if edge.CalleeID == id {
					neighbor = edge.CallerID
				}
				function := index.Functions[neighbor]
				if function.Test && !includeTests || selected[neighbor] {
					continue
				}
				selected[neighbor] = true
				next = append(next, neighbor)
			}
		}
		frontier = next
	}

	nodes := make([]Function, 0, len(selected))
	for id := range selected {
		nodes = append(nodes, index.Functions[id])
	}
	sort.Slice(nodes, func(left, right int) bool { return nodes[left].QualifiedName < nodes[right].QualifiedName })
	edges := make([]Edge, 0)
	for _, edge := range index.Edges {
		if selected[edge.CallerID] && selected[edge.CalleeID] {
			edges = append(edges, edge)
		}
	}
	return Graph{Root: root.ID, Nodes: nodes, Edges: edges}, nil
}
