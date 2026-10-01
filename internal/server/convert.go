package server

import (
	"path/filepath"
	"strconv"

	"github.com/gtindo/flowmap/internal/analyzer"
	"github.com/gtindo/flowmap/internal/protocol"
)

// The browser workbench keeps its snake_case, path-based HTTP models; these
// functions translate camelCase, URI-based protocol models back into them.

// searchResults converts protocol summaries into browser search rows.
// Operations (Pure): data mapping only.
func searchResults(items []protocol.SymbolSummary) []analyzer.SearchResult {
	results := make([]analyzer.SearchResult, 0, len(items))
	for _, item := range items {
		results = append(results, analyzer.SearchResult{
			ID:             item.SymbolID,
			QualifiedName:  item.QualifiedName,
			Package:        item.Namespace,
			Signature:      item.Signature,
			Classification: item.Classification,
			Test:           item.Test,
		})
	}
	return results
}

// functionModel converts a protocol symbol into the browser function model.
// Operations (Pure): data mapping only.
func functionModel(symbol protocol.Symbol) analyzer.Function {
	function := analyzer.Function{
		ID:            symbol.SymbolID,
		Name:          symbol.Name,
		Kind:          symbol.Kind,
		QualifiedName: symbol.QualifiedName,
		Package:       symbol.Namespace,
		Language:      symbol.Language,
		Signature:     symbol.Signature,
		Parameters:    symbol.Parameters,
		Results:       symbol.Results,
		Contracts:     contractModels(symbol.Contracts),
		Intent:        symbol.Intent,
		IntentSource:  symbol.IntentSource,
		File:          uriPath(symbol.Location.URI),
		Line:          symbol.Location.StartLine,
		EndLine:       symbol.Location.EndLine,
		Source:        symbol.Source,
		Public:        symbol.Public,
		Test:          symbol.Test,
		Anonymous:     symbol.Anonymous,
		Classification: analyzer.Classification{
			Kind:       symbol.ClassificationDetail.Kind,
			Provenance: symbol.ClassificationDetail.Provenance,
			Evidence:   symbol.ClassificationDetail.Evidence,
		},
	}

	if symbol.Change != nil {
		function.Change = &analyzer.FunctionChange{Kind: symbol.Change.Kind, Diff: symbol.Change.Diff}
	}
	return function
}

func contractModels(contracts []protocol.Contract) []analyzer.Contract {
	result := make([]analyzer.Contract, 0, len(contracts))
	for _, contract := range contracts {
		fields := make([]analyzer.Field, 0, len(contract.Fields))
		for _, field := range contract.Fields {
			fields = append(fields, analyzer.Field{Name: field.Name, Type: field.Type})
		}
		result = append(result, analyzer.Contract{Name: contract.Name, Kind: contract.Kind, Fields: fields, Methods: contract.Methods})
	}
	return result
}

// graphModel converts a protocol neighborhood into the browser graph model.
// Operations (Pure): data mapping only.
func graphModel(neighborhood protocol.Graph, root string) analyzer.Graph {
	nodes := make([]analyzer.Function, 0, len(neighborhood.Nodes))
	for _, node := range neighborhood.Nodes {
		nodes = append(nodes, functionModel(node))
	}

	edges := make([]analyzer.Edge, 0, len(neighborhood.Edges))
	for _, edge := range neighborhood.Edges {
		callSite := ""
		if edge.CallSite != nil {
			callSite = uriPath(edge.CallSite.URI) + ":" + strconv.Itoa(edge.CallSite.StartLine)
		}
		edges = append(edges, analyzer.Edge{CallerID: edge.FromSymbolID, CalleeID: edge.ToSymbolID, Kind: edge.Kind, Dynamic: edge.Dynamic, CallSite: callSite})
	}

	return analyzer.Graph{Root: neighborhood.RootSymbolID, Nodes: nodes, Edges: edges}
}

// gitSnapshotModel converts captured Git state into the browser model.
// Operations (Pure): data mapping only.
func gitSnapshotModel(state protocol.GitState, changes []protocol.GitChange) analyzer.GitSnapshot {
	changed := make([]analyzer.ChangedFunction, 0, len(changes))
	for _, change := range changes {
		changed = append(changed, analyzer.ChangedFunction{
			ID:                  change.SymbolID,
			QualifiedName:       change.QualifiedName,
			Package:             change.Namespace,
			File:                uriPath(change.Location.URI),
			Line:                change.Location.StartLine,
			Test:                change.Test,
			Kind:                change.Kind,
			LeafDescendantCount: change.LeafDescendantCount,
		})
	}

	return analyzer.GitSnapshot{Available: state.Available, Branch: state.Branch, Detached: state.Detached, Revision: state.Revision, ChangedFunctions: changed}
}

// loadReportModel rebuilds the analyzer load report so the browser and CLI
// warnings keep their existing rendering.
// Operations (Pure): data mapping only.
func loadReportModel(report protocol.LoadReport, items []protocol.Diagnostic, root string) analyzer.LoadReport {
	result := analyzer.LoadReport{
		Root:        root,
		Language:    report.Language,
		BuildTags:   report.BuildTags,
		TotalUnits:  report.TotalUnits,
		FailedUnits: report.FailedUnits,
		Diagnostics: make([]analyzer.LoadDiagnostic, 0, len(items)),
	}
	isGo := report.Language == "" || report.Language == analyzer.LanguageGo
	if isGo {
		result.TotalPackageVariants, result.FailedPackageVariants = report.TotalUnits, report.FailedUnits
	}

	for _, item := range items {
		diagnostic := analyzer.LoadDiagnostic{Kind: item.Kind, Message: item.Message, Units: item.AffectedUnits}
		if isGo {
			diagnostic.Packages = item.AffectedUnits
		}
		if item.Location != nil {
			diagnostic.Position = displayPath(root, uriPath(item.Location.URI)) + ":" + strconv.Itoa(item.Location.StartLine)
		}
		result.Diagnostics = append(result.Diagnostics, diagnostic)
	}
	return result
}

// uriPath returns the filesystem path for a file URI, or the URI itself when
// it cannot be converted, so display never fails.
func uriPath(uri string) string {
	path, err := protocol.PathFromURI(uri)
	if err != nil {
		return uri
	}
	return path
}

func displayPath(root string, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || len(relative) > 2 && relative[:3] == ".."+string(filepath.Separator) {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(relative)
}
