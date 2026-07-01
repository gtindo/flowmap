package analyzer

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestObservyJourneys validates the external acceptance fixture when explicitly configured.
func TestObservyJourneys(t *testing.T) {
	root := os.Getenv("FLOWMAP_ACCEPTANCE_ROOT")
	if root == "" {
		t.Skip("FLOWMAP_ACCEPTANCE_ROOT is not configured")
	}
	index, err := Analyze(context.Background(), Config{Root: root})
	if err != nil {
		t.Fatalf("Analyze(Observy) error = %v", err)
	}
	assertJourney(t, index, "/ingestion", "LogServer).Export", "parseIncomingLogRequest")
	assertJourney(t, index, "/query/executor", "ExecuteOQL", "executeSQLite")
	assertJourney(t, index, "/web/features/metrics", "handleMetricsExplorer", "BuildInitialMetricsView")
}

// assertJourney confirms a named entry can reach an expected downstream operation.
func assertJourney(t *testing.T, index *Index, packageFragment string, entryName string, expectedName string) {
	t.Helper()
	var entry Function
	for _, function := range index.Functions {
		if strings.Contains(function.Package, packageFragment) && strings.HasSuffix(function.QualifiedName, entryName) {
			entry = function
			break
		}
	}
	if entry.ID == "" {
		t.Fatalf("entry %s:%s not found", packageFragment, entryName)
	}
	graph, err := index.Focus(entry.ID, "downstream", 5, false)
	if err != nil {
		t.Fatalf("Focus(%s) error = %v", entry.QualifiedName, err)
	}
	for _, function := range graph.Nodes {
		if strings.Contains(function.QualifiedName, expectedName) {
			return
		}
	}
	t.Fatalf("journey from %s did not reach %s (%d nodes)", entry.QualifiedName, expectedName, len(graph.Nodes))
}
