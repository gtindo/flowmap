package analyzer

import (
	"reflect"
	"testing"
)

func TestOrderChangedFunctionsUsesChangedLeafHierarchy(t *testing.T) {
	changed := []ChangedFunction{
		{ID: "leaf-b", QualifiedName: "sample.LeafB"},
		{ID: "solo", QualifiedName: "sample.Solo"},
		{ID: "root", QualifiedName: "sample.Root"},
		{ID: "leaf-a", QualifiedName: "sample.LeafA"},
		{ID: "middle", QualifiedName: "sample.Middle"},
	}
	edges := []Edge{
		{CallerID: "root", CalleeID: "helper", Kind: edgeKindCall},
		{CallerID: "helper", CalleeID: "middle", Kind: edgeKindDependency},
		{CallerID: "middle", CalleeID: "leaf-a", Kind: edgeKindCall},
		{CallerID: "root", CalleeID: "leaf-b", Kind: edgeKindCall},
		{CallerID: "helper", CalleeID: "leaf-b", Kind: edgeKindCall},
	}

	ordered := orderChangedFunctions(GitSnapshot{ChangedFunctions: changed}, outgoingEdges(edges)).ChangedFunctions

	wantIDs := []string{"root", "solo", "middle", "leaf-b", "leaf-a"}
	if got := changedIDs(ordered); !reflect.DeepEqual(got, wantIDs) {
		t.Fatalf("changed order = %v, want %v", got, wantIDs)
	}
	wantCounts := map[string]int{"root": 2, "middle": 1, "leaf-a": 0, "leaf-b": 0, "solo": 0}
	for _, function := range ordered {
		if function.LeafDescendantCount != wantCounts[function.ID] {
			t.Errorf("%s leaf count = %d, want %d", function.ID, function.LeafDescendantCount, wantCounts[function.ID])
		}
	}
}

func TestOrderChangedFunctionsCollapsesRecursiveComponents(t *testing.T) {
	changed := []ChangedFunction{
		{ID: "cycle-b", QualifiedName: "sample.CycleB"},
		{ID: "leaf", QualifiedName: "sample.Leaf"},
		{ID: "cycle-a", QualifiedName: "sample.CycleA"},
	}
	edges := []Edge{
		{CallerID: "cycle-a", CalleeID: "cycle-b", Kind: edgeKindCall},
		{CallerID: "cycle-b", CalleeID: "cycle-a", Kind: edgeKindCall},
		{CallerID: "cycle-b", CalleeID: "unchanged", Kind: edgeKindDependency},
		{CallerID: "unchanged", CalleeID: "leaf", Kind: edgeKindCall},
	}

	ordered := orderChangedFunctions(GitSnapshot{ChangedFunctions: changed}, outgoingEdges(edges)).ChangedFunctions

	wantIDs := []string{"cycle-a", "cycle-b", "leaf"}
	if got := changedIDs(ordered); !reflect.DeepEqual(got, wantIDs) {
		t.Fatalf("changed order = %v, want %v", got, wantIDs)
	}
	if ordered[0].LeafDescendantCount != 1 || ordered[1].LeafDescendantCount != 1 || ordered[2].LeafDescendantCount != 0 {
		t.Fatalf("leaf counts = %#v", ordered)
	}
}

func TestOrderChangedFunctionsDeduplicatesLeafPathsAndUsesStableTies(t *testing.T) {
	changed := []ChangedFunction{
		{ID: "z-root", QualifiedName: "sample.ZRoot"},
		{ID: "leaf", QualifiedName: "sample.Leaf"},
		{ID: "a-root", QualifiedName: "sample.ARoot"},
	}
	edges := []Edge{
		{CallerID: "a-root", CalleeID: "left", Kind: edgeKindCall},
		{CallerID: "a-root", CalleeID: "right", Kind: edgeKindDependency},
		{CallerID: "left", CalleeID: "leaf", Kind: edgeKindCall},
		{CallerID: "right", CalleeID: "leaf", Kind: edgeKindCall},
		{CallerID: "z-root", CalleeID: "leaf", Kind: edgeKindCall},
	}

	ordered := orderChangedFunctions(GitSnapshot{ChangedFunctions: changed}, outgoingEdges(edges)).ChangedFunctions

	wantIDs := []string{"a-root", "z-root", "leaf"}
	if got := changedIDs(ordered); !reflect.DeepEqual(got, wantIDs) {
		t.Fatalf("changed order = %v, want %v", got, wantIDs)
	}
	if ordered[0].LeafDescendantCount != 1 {
		t.Fatalf("duplicate paths produced leaf count %d, want 1", ordered[0].LeafDescendantCount)
	}
}

func outgoingEdges(edges []Edge) map[string][]Edge {
	result := make(map[string][]Edge)
	for _, edge := range edges {
		result[edge.CallerID] = append(result[edge.CallerID], edge)
	}
	return result
}

func changedIDs(functions []ChangedFunction) []string {
	result := make([]string, 0, len(functions))
	for _, function := range functions {
		result = append(result, function.ID)
	}
	return result
}
