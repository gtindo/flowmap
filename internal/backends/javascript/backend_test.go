package javascript

import (
	"context"
	"os"
	"path/filepath"
	"sort"
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

func TestBackendCollectsClassCallablesAndConservativeMemberEdges(t *testing.T) {
	root := t.TempDir()
	writeJavaScriptFixture(t, root, "api.ts", `export function fetchUser() {}
export { fetchUser as fetchAlias };
`)
	writeJavaScriptFixture(t, root, "common.cjs", `function fetchCommon() {}
module.exports = { fetchCommon };
`)
	writeJavaScriptFixture(t, root, "models.ts", `export class Parent {
  save() {}
}

/** Service intent. */
export class UserService {
  /** Construct a service. */
  constructor(name: string) {}
  get label() { return "user"; }
  set label(value: string) {}
  async save() {}
  *entries() { yield 1; }
  static create() { return new UserService("new"); }
  run() { this.save(); }
}

export class Duplicate { save() {} }

export const ExpressionService = class {
  save() {}
}

export default class DefaultService {
  save() {}
}

export function create(): UserService { return new UserService("factory"); }
`)
	writeJavaScriptFixture(t, root, "app.ts", `import * as api from "./api";
import { UserService, Parent, create } from "./models";
const common = require("./common");

function namespaceCalls() { api.fetchAlias(); common.fetchCommon(); }
function constructed() { const service = new UserService("x"); service.save(); }
function typed(service: UserService) { service.save(); }
function aliases() { let service: UserService; const copy = service; copy.save(); }
function fromFactory() { const service = create(); service.save(); }
function unknown(service: any, external: External, union: UserService | Parent) { service.save(); external.save(); union.save(); }
function computed(service: UserService) { service["save"](); }
class Controller {
  service: UserService;
  run() { this.service.save(); }
}
class Child extends Parent {
  run() { super.save(); }
}
class StaticCaller {
  run() { UserService.create(); }
}
`)

	snapshot, err := (Backend{}).Analyze(context.Background(), semantic.AnalysisRequest{Root: root, Language: "javascript"})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	byName := symbolsByName(snapshot)
	for _, name := range []string{"UserService.constructor", "UserService.label", "UserService.save", "UserService.entries", "UserService.create", "Duplicate.save", "ExpressionService.save", "DefaultService.save", "Child.run"} {
		if byName[name] == nil {
			t.Fatalf("missing class callable %q; symbols = %v", name, sortedSymbolNames(snapshot))
		}
	}
	if byName["UserService.constructor"].Documentation == "" {
		t.Fatal("constructor omitted JSDoc")
	}
	if byName["UserService.save"].ID == byName["Duplicate.save"].ID {
		t.Fatal("methods with the same name must have distinct stable identities")
	}

	assertRelationship(t, snapshot, byName, "UserService.run", "UserService.save", semantic.RelationshipCall, false)
	assertRelationship(t, snapshot, byName, "Child.run", "Parent.save", semantic.RelationshipCall, false)
	assertRelationship(t, snapshot, byName, "StaticCaller.run", "UserService.create", semantic.RelationshipCall, false)
	assertRelationship(t, snapshot, byName, "namespaceCalls", "fetchUser", semantic.RelationshipCall, false)
	assertRelationship(t, snapshot, byName, "namespaceCalls", "fetchCommon", semantic.RelationshipCall, false)
	for _, caller := range []string{"constructed", "typed", "aliases", "fromFactory", "Controller.run"} {
		assertRelationship(t, snapshot, byName, caller, "UserService.save", semantic.RelationshipDependency, true)
	}
	assertNoRelationshipFrom(t, snapshot, byName["unknown"].ID)
	assertNoRelationshipFrom(t, snapshot, byName["computed"].ID)
}

func writeJavaScriptFixture(t *testing.T, root, name, source string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
}

func symbolsByName(snapshot semantic.Snapshot) map[string]*semantic.Symbol {
	result := make(map[string]*semantic.Symbol, len(snapshot.Symbols))
	for index := range snapshot.Symbols {
		symbol := &snapshot.Symbols[index]
		result[symbol.Name] = symbol
	}
	return result
}

func sortedSymbolNames(snapshot semantic.Snapshot) []string {
	names := make([]string, 0, len(snapshot.Symbols))
	for _, symbol := range snapshot.Symbols {
		names = append(names, symbol.Name)
	}
	sort.Strings(names)
	return names
}

func assertRelationship(t *testing.T, snapshot semantic.Snapshot, symbols map[string]*semantic.Symbol, from, to, kind string, dynamic bool) {
	t.Helper()
	for _, relationship := range snapshot.Relationships {
		if relationship.FromID == symbols[from].ID && relationship.ToID == symbols[to].ID && relationship.Kind == kind && relationship.Dynamic == dynamic {
			return
		}
	}
	t.Fatalf("missing %s relationship %s -> %s (dynamic %t): %#v", kind, from, to, dynamic, snapshot.Relationships)
}

func assertNoRelationshipFrom(t *testing.T, snapshot semantic.Snapshot, id string) {
	t.Helper()
	for _, relationship := range snapshot.Relationships {
		if relationship.FromID == id {
			t.Fatalf("unexpected relationship %#v", relationship)
		}
	}
}
