package javascript

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gtindo/flowmap/internal/semantic"
)

func TestBackendCollectsLocalAndRelativeCalls(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "math.ts"), []byte("export function add(left: number, right: number) { return left + right; }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app.ts"), []byte("import { add as sum } from './math';\n/** Operations (Pure) */\nexport const total = (value: number) => sum(value, 1);\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	snapshot, err := (Backend{}).Analyze(context.Background(), semantic.AnalysisRequest{Root: root, Language: "javascript"})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if snapshot.Language != "javascript" || len(snapshot.Symbols) != 2 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if len(snapshot.Relationships) != 1 || snapshot.Relationships[0].Kind != semantic.RelationshipCall {
		t.Fatalf("relationships = %#v", snapshot.Relationships)
	}
	for _, symbol := range snapshot.Symbols {
		if symbol.Name == "total" && symbol.Documentation == "" {
			t.Fatal("total omitted JSDoc")
		}
	}
}
