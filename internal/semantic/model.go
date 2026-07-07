// Package semantic defines language-neutral facts produced by analysis backends.
package semantic

import "context"

// Backend analyzes one repository into language-neutral semantic facts.
type Backend interface {
	Analyze(context.Context, AnalysisRequest) (Snapshot, error)
}

// AnalysisRequest describes one repository analysis.
type AnalysisRequest struct {
	Root      string
	BuildTags []string
}

// Snapshot is a complete set of backend facts for one repository state.
type Snapshot struct {
	Root          string
	Symbols       []Symbol
	Relationships []Relationship
	Diagnostics   DiagnosticReport
}

// Symbol describes one callable repository symbol.
type Symbol struct {
	ID            string
	Kind          string
	Name          string
	QualifiedName string
	Package       string
	Location      Location
	Source        string
	Signature     Signature
	Documentation string
	Test          bool
	Facts         []Fact
}

// Location identifies a source span.
type Location struct {
	File    string
	Line    int
	EndLine int
}

// Signature describes a callable boundary without compiler-specific types.
type Signature struct {
	Display    string
	Parameters []string
	Results    []string
	Contracts  []Contract
}

// Field describes one field in a structural contract.
type Field struct {
	Name string
	Type string
}

// Contract describes a named type crossing a callable boundary.
type Contract struct {
	Name    string
	Kind    string
	Fields  []Field
	Methods []string
}

// Fact is backend evidence that Flowmap may interpret during enrichment.
type Fact struct {
	Kind    string
	Package string
	Name    string
}

const (
	// SymbolFunction identifies a named function.
	SymbolFunction = "function"
	// SymbolMethod identifies a named method.
	SymbolMethod = "method"
	// SymbolClosure identifies an anonymous function.
	SymbolClosure = "closure"

	// FactDeclarationWithoutBody identifies a callable whose effects are not source-visible.
	FactDeclarationWithoutBody = "declaration_without_body"
	// FactStartsConcurrentWork identifies explicit concurrent execution.
	FactStartsConcurrentWork = "starts_concurrent_work"
	// FactChannelSend identifies a send operation.
	FactChannelSend = "channel_send"
	// FactWritesPackageState identifies a package/global state write.
	FactWritesPackageState = "writes_package_state"
	// FactWritesObjectState identifies an object-field write.
	FactWritesObjectState = "writes_object_state"
	// FactWritesIndexedState identifies an indexed collection write.
	FactWritesIndexedState = "writes_indexed_state"
	// FactExternalCall identifies a call outside the analyzed snapshot.
	FactExternalCall = "external_call"
)

// Relationship describes a directed semantic connection between symbols.
type Relationship struct {
	FromID     string
	ToID       string
	Kind       string
	Location   string
	Provenance string
	Precision  string
	Dynamic    bool
}

const (
	// RelationshipCall identifies a call relationship.
	RelationshipCall = "call"
	// RelationshipDependency identifies a function passed or returned as a dependency.
	RelationshipDependency = "dependency"
)

// DiagnosticReport summarizes backend loading failures.
type DiagnosticReport struct {
	BuildTags   []string
	TotalUnits  int
	FailedUnits int
	Diagnostics []Diagnostic
}

// Diagnostic is one deduplicated backend problem and its affected units.
type Diagnostic struct {
	Kind     string
	Position string
	Message  string
	Units    []string
}
