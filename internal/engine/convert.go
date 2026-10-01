package engine

import (
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/gtindo/flowmap/internal/analyzer"
	"github.com/gtindo/flowmap/internal/protocol"
)

// symbolSummary converts an analyzer function into the compact wire model.
// Operations (Pure): data mapping only.
func symbolSummary(function analyzer.Function) protocol.SymbolSummary {
	return protocol.SymbolSummary{
		SymbolID:       function.ID,
		Name:           function.Name,
		QualifiedName:  function.QualifiedName,
		Namespace:      function.Package,
		Language:       function.Language,
		Signature:      function.Signature,
		Classification: function.Classification.Kind,
		Public:         function.Public,
		Test:           function.Test,
	}
}

// symbol converts an analyzer function into the full wire model.
// Operations (Pure): data mapping only.
func symbol(function analyzer.Function) protocol.Symbol {
	result := protocol.Symbol{
		SymbolSummary: symbolSummary(function),
		Kind:          symbolKind(function),
		Location:      sourceLocation(function.File, function.Line, function.EndLine),
		Parameters:    nonNilStrings(function.Parameters),
		Results:       nonNilStrings(function.Results),
		Contracts:     contracts(function.Contracts),
		Intent:        function.Intent,
		IntentSource:  function.IntentSource,
		Source:        function.Source,
		Anonymous:     function.Anonymous,
		ClassificationDetail: protocol.Classification{
			Kind:       function.Classification.Kind,
			Provenance: function.Classification.Provenance,
			Evidence:   nonNilStrings(function.Classification.Evidence),
		},
	}

	if function.Change != nil {
		result.Change = &protocol.SymbolChange{Kind: function.Change.Kind, Diff: function.Change.Diff}
	}
	return result
}

func symbolKind(function analyzer.Function) string {
	if function.Kind != "" {
		return function.Kind
	}
	if function.Anonymous {
		return "closure"
	}
	return "function"
}

func contracts(values []analyzer.Contract) []protocol.Contract {
	result := make([]protocol.Contract, 0, len(values))
	for _, contract := range values {
		fields := make([]protocol.ContractField, 0, len(contract.Fields))
		for _, field := range contract.Fields {
			fields = append(fields, protocol.ContractField{Name: field.Name, Type: field.Type})
		}

		result = append(result, protocol.Contract{
			Name:    contract.Name,
			Kind:    contract.Kind,
			Fields:  fields,
			Methods: nonNilStrings(contract.Methods),
		})
	}
	return result
}

// sourceLocation builds a valid one-based inclusive span from analyzer data.
// Lines below one are clamped because zero is invalid on the wire.
func sourceLocation(path string, line int, endLine int) protocol.SourceLocation {
	startLine := max(line, 1)
	return protocol.SourceLocation{URI: protocol.FileURI(path), StartLine: startLine, EndLine: max(endLine, startLine)}
}

// graph converts a focused analyzer graph into the wire model.
// Operations (Pure): data mapping only.
func graph(focused analyzer.Graph, root string) protocol.Graph {
	nodes := make([]protocol.Symbol, 0, len(focused.Nodes))
	for _, node := range focused.Nodes {
		nodes = append(nodes, symbol(node))
	}

	edges := make([]protocol.Edge, 0, len(focused.Edges))
	for _, edge := range focused.Edges {
		edges = append(edges, protocol.Edge{
			FromSymbolID: edge.CallerID,
			ToSymbolID:   edge.CalleeID,
			Kind:         edge.Kind,
			Dynamic:      edge.Dynamic,
			CallSite:     positionLocation(root, edge.CallSite),
		})
	}

	return protocol.Graph{RootSymbolID: focused.Root, Nodes: nodes, Edges: edges}
}

// positionLocation parses "path:line" or "path:line:column" positions.
// Relative paths resolve beneath root. Unparseable positions yield nil.
// Operations (Pure): string parsing only.
func positionLocation(root string, position string) *protocol.SourceLocation {
	remaining := strings.TrimSpace(position)
	numbers := make([]int, 0, 2)
	for len(numbers) < 2 {
		separator := strings.LastIndex(remaining, ":")
		if separator < 0 {
			break
		}
		number, err := strconv.Atoi(remaining[separator+1:])
		if err != nil {
			break
		}
		numbers = append(numbers, number)
		remaining = remaining[:separator]
	}

	if len(numbers) == 0 || remaining == "" || remaining == "-" {
		return nil
	}

	line := numbers[len(numbers)-1]
	if line < 1 {
		return nil
	}

	path := filepath.FromSlash(remaining)
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}

	return &protocol.SourceLocation{URI: protocol.FileURI(path), StartLine: line, EndLine: line}
}

// gitState converts the captured Git snapshot header.
// Operations (Pure): data mapping only.
func gitState(snapshot analyzer.GitSnapshot) protocol.GitState {
	state := protocol.GitState{Available: snapshot.Available, Detached: snapshot.Detached}
	if snapshot.Available {
		state.Branch = snapshot.Branch
		state.Revision = snapshot.Revision
	}
	return state
}

// gitChanges converts changed functions in engine review order.
// Operations (Pure): data mapping only.
func gitChanges(index *analyzer.Index) []protocol.GitChange {
	if !index.Git.Available {
		return []protocol.GitChange{}
	}

	result := make([]protocol.GitChange, 0, len(index.Git.ChangedFunctions))
	for _, changed := range index.Git.ChangedFunctions {
		function := index.Functions[changed.ID]
		diff := ""
		if function.Change != nil {
			diff = function.Change.Diff
		}

		result = append(result, protocol.GitChange{
			SymbolID:            changed.ID,
			QualifiedName:       changed.QualifiedName,
			Namespace:           changed.Package,
			Location:            sourceLocation(changed.File, changed.Line, function.EndLine),
			Test:                changed.Test,
			Kind:                changed.Kind,
			Diff:                diff,
			LeafDescendantCount: changed.LeafDescendantCount,
		})
	}
	return result
}

// loadReport converts the snapshot's load report header.
// Operations (Pure): data mapping only.
func loadReport(report analyzer.LoadReport, language string) protocol.LoadReport {
	totalUnits, failedUnits := report.TotalUnits, report.FailedUnits
	if totalUnits == 0 && failedUnits == 0 {
		totalUnits, failedUnits = report.TotalPackageVariants, report.FailedPackageVariants
	}

	reportLanguage := report.Language
	if reportLanguage == "" {
		reportLanguage = language
	}

	return protocol.LoadReport{
		Language:        reportLanguage,
		TotalUnits:      totalUnits,
		FailedUnits:     failedUnits,
		BuildTags:       nonNilStrings(report.BuildTags),
		DiagnosticCount: len(report.Diagnostics),
	}
}

// diagnostics converts backend load diagnostics.
// Operations (Pure): data mapping only.
func diagnostics(report analyzer.LoadReport, root string) []protocol.Diagnostic {
	result := make([]protocol.Diagnostic, 0, len(report.Diagnostics))
	for _, diagnostic := range report.Diagnostics {
		units := diagnostic.Units
		if len(units) == 0 {
			units = diagnostic.Packages
		}

		result = append(result, protocol.Diagnostic{
			Kind:          diagnostic.Kind,
			Message:       diagnostic.Message,
			AffectedUnits: nonNilStrings(units),
			Location:      positionLocation(root, diagnostic.Position),
		})
	}
	return result
}

// searchSymbols returns every matching summary in a total, stable order so
// pagination cursors address the same sequence on every request.
// Operations (Pure): reads an immutable index.
func searchSymbols(index *analyzer.Index, query string, includeTests bool) []protocol.SymbolSummary {
	matches := index.Search(query, includeTests, len(index.Functions)+1)
	sort.SliceStable(matches, func(left, right int) bool {
		if matches[left].QualifiedName != matches[right].QualifiedName {
			return matches[left].QualifiedName < matches[right].QualifiedName
		}
		return matches[left].ID < matches[right].ID
	})

	result := make([]protocol.SymbolSummary, 0, len(matches))
	for _, match := range matches {
		result = append(result, symbolSummary(index.Functions[match.ID]))
	}
	return result
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return append([]string(nil), values...)
}

// cursor binds a page offset to the exact request that produced it.
type cursor struct {
	Method     string `json:"m"`
	ViewID     string `json:"v"`
	SnapshotID string `json:"s"`
	Key        string `json:"k"`
	Offset     int    `json:"o"`
}

// pageBounds validates a page request and returns the item range and the
// next cursor, if any. The cursor is opaque but self-validating.
// Operations (Pure): arithmetic and encoding only.
func pageBounds(page *protocol.Page, binding cursor, total int) (int, int, string, *protocol.Error) {
	limit := protocol.DefaultPageLimit
	offset := 0

	if page != nil {
		if page.Limit != 0 {
			limit = page.Limit
		}
		if limit < 1 || limit > protocol.MaxPageLimit {
			return 0, 0, "", protocol.Errorf(protocol.CodeInvalidParams, "page.limit must be between 1 and %d", protocol.MaxPageLimit)
		}

		if page.Cursor != "" {
			decoded, ok := decodeCursor(page.Cursor)
			if !ok || decoded.Method != binding.Method || decoded.ViewID != binding.ViewID || decoded.SnapshotID != binding.SnapshotID || decoded.Key != binding.Key || decoded.Offset < 0 || decoded.Offset > total {
				return 0, 0, "", protocol.Errorf(protocol.CodeInvalidParams, "page.cursor is invalid for this request")
			}
			offset = decoded.Offset
		}
	}

	end := min(offset+limit, total)
	next := ""
	if end < total {
		binding.Offset = end
		next = encodeCursor(binding)
	}
	return offset, end, next, nil
}

func encodeCursor(value cursor) string {
	encoded, _ := json.Marshal(value)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeCursor(value string) (cursor, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return cursor{}, false
	}

	var result cursor
	if err := json.Unmarshal(decoded, &result); err != nil {
		return cursor{}, false
	}
	return result, true
}
