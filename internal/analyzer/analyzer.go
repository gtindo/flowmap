package analyzer

import (
	"context"
	"fmt"
	"strings"

	gobackend "github.com/gtindo/flowmap/internal/backends/go"
	"github.com/gtindo/flowmap/internal/semantic"
)

// Config controls one repository analysis without mutating the target tree.
type Config struct {
	Root      string
	BuildTags []string
}

type functionMeta struct {
	function     Function
	directEdge   bool
	localCalls   []string
	externalCall bool
}

const (
	edgeKindCall       = "call"
	edgeKindDependency = "dependency"
)

// Analyze loads a Go working tree through the built-in backend and enriches its
// language-neutral semantic snapshot into Flowmap's immutable index.
func Analyze(ctx context.Context, config Config) (*Index, error) {
	return analyzeWithBackend(ctx, config, gobackend.Backend{})
}

// AnalyzeWithBackend enriches one backend snapshot into Flowmap's immutable index.
// Side Effect (Edge): the backend and Git attribution may access repository state.
func AnalyzeWithBackend(ctx context.Context, config Config, backend semantic.Backend) (*Index, error) {
	if backend == nil {
		return nil, fmt.Errorf("analyze repository: semantic backend is required")
	}

	index, err := analyzeWithBackend(ctx, config, backend)
	if err != nil {
		return nil, fmt.Errorf("analyze repository with semantic backend: %w", err)
	}
	return index, nil
}

func analyzeWithBackend(ctx context.Context, config Config, backend semantic.Backend) (*Index, error) {
	request := semantic.AnalysisRequest{Root: config.Root, BuildTags: append([]string(nil), config.BuildTags...)}
	snapshot, err := backend.Analyze(ctx, request)
	if err != nil {
		return nil, err
	}

	index := buildIndex(snapshot)
	index.Git = captureGitSnapshot(ctx, snapshot.Root, index.Functions)
	index.Git = orderChangedFunctions(index.Git, index.Outgoing)

	return index, nil
}

// buildIndex is the deterministic semantic-to-Flowmap enrichment stage.
// Operations (Pure): transforms a complete snapshot without I/O or hidden state.
func buildIndex(snapshot semantic.Snapshot) *Index {
	metas := make(map[string]*functionMeta, len(snapshot.Symbols))
	for _, symbol := range snapshot.Symbols {
		classification, directEdge, externalCall := classifyDirect(symbol.Documentation, symbol.Facts)
		metas[symbol.ID] = &functionMeta{
			directEdge: directEdge, externalCall: externalCall,
			function: Function{
				ID: symbol.ID, Name: symbol.Name, QualifiedName: symbol.QualifiedName,
				Package: symbol.Package, Signature: symbol.Signature.Display,
				Parameters: append([]string(nil), symbol.Signature.Parameters...),
				Results:    append([]string(nil), symbol.Signature.Results...),
				Contracts:  contractsFromSemantic(symbol.Signature.Contracts),
				Intent:     firstParagraph(symbol.Documentation), IntentSource: intentSource(symbol.Documentation),
				File: symbol.Location.File, Line: symbol.Location.Line, EndLine: symbol.Location.EndLine,
				Source: symbol.Source, Test: symbol.Test, Anonymous: symbol.Kind == semantic.SymbolClosure,
				Classification: classification,
			},
		}
	}

	edges := edgesFromSemantic(snapshot.Relationships)
	classifyFunctions(metas, edges)
	index := &Index{
		Root: snapshot.Root, Functions: make(map[string]Function, len(metas)), Edges: edges,
		Outgoing: make(map[string][]Edge), Incoming: make(map[string][]Edge),
		LoadReport: loadReportFromSemantic(snapshot.Root, snapshot.Diagnostics),
	}
	for id, meta := range metas {
		index.Functions[id] = meta.function
	}
	for _, edge := range edges {
		index.Outgoing[edge.CallerID] = append(index.Outgoing[edge.CallerID], edge)
		index.Incoming[edge.CalleeID] = append(index.Incoming[edge.CalleeID], edge)
	}
	return index
}

func contractsFromSemantic(contracts []semantic.Contract) []Contract {
	result := make([]Contract, 0, len(contracts))
	for _, contract := range contracts {
		fields := make([]Field, 0, len(contract.Fields))
		for _, field := range contract.Fields {
			fields = append(fields, Field{Name: field.Name, Type: field.Type})
		}
		result = append(result, Contract{
			Name: contract.Name, Kind: contract.Kind, Fields: fields, Methods: append([]string(nil), contract.Methods...),
		})
	}
	return result
}

func edgesFromSemantic(relationships []semantic.Relationship) []Edge {
	edges := make([]Edge, 0, len(relationships))
	for _, relationship := range relationships {
		edges = append(edges, Edge{
			CallerID: relationship.FromID, CalleeID: relationship.ToID, Kind: relationship.Kind,
			Dynamic: relationship.Dynamic, CallSite: relationship.Location,
		})
	}
	return edges
}

func firstParagraph(documentation string) string {
	documentation = strings.TrimSpace(documentation)
	if split := strings.Index(documentation, "\n\n"); split >= 0 {
		return strings.TrimSpace(documentation[:split])
	}
	return documentation
}

func intentSource(documentation string) string {
	if firstParagraph(documentation) == "" {
		return ""
	}
	return "documentation"
}
