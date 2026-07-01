// Package analyzer builds a typed, evidence-rich map of a Go codebase.
package analyzer

// Classification describes a function's relationship to side effects.
type Classification struct {
	Kind       string   `json:"kind"`
	Provenance string   `json:"provenance"`
	Evidence   []string `json:"evidence"`
}

// Field describes one field of a named struct contract.
type Field struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Contract describes a named type crossing a function boundary.
type Contract struct {
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	Fields  []Field  `json:"fields,omitempty"`
	Methods []string `json:"methods,omitempty"`
}

// Function is the stable browser representation of an analyzed function.
type Function struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	QualifiedName  string         `json:"qualified_name"`
	Package        string         `json:"package"`
	Signature      string         `json:"signature"`
	Parameters     []string       `json:"parameters"`
	Results        []string       `json:"results"`
	Contracts      []Contract     `json:"contracts,omitempty"`
	Intent         string         `json:"intent,omitempty"`
	IntentSource   string         `json:"intent_source,omitempty"`
	File           string         `json:"file"`
	Line           int            `json:"line"`
	EndLine        int            `json:"end_line"`
	Source         string         `json:"source,omitempty"`
	Test           bool           `json:"test"`
	Classification Classification `json:"classification"`
}

// Edge represents one possible call between local functions.
type Edge struct {
	CallerID string `json:"caller_id"`
	CalleeID string `json:"callee_id"`
	Dynamic  bool   `json:"dynamic"`
	CallSite string `json:"call_site,omitempty"`
}

// Graph is a focused subgraph returned to the browser.
type Graph struct {
	Root  string     `json:"root"`
	Nodes []Function `json:"nodes"`
	Edges []Edge     `json:"edges"`
}

// Index is an immutable analysis result safe for concurrent readers.
type Index struct {
	Root       string
	Functions  map[string]Function
	Edges      []Edge
	Outgoing   map[string][]Edge
	Incoming   map[string][]Edge
	LoadReport LoadReport
}

// SearchResult is the compact representation used by symbol search.
type SearchResult struct {
	ID             string `json:"id"`
	QualifiedName  string `json:"qualified_name"`
	Package        string `json:"package"`
	Signature      string `json:"signature"`
	Classification string `json:"classification"`
	Test           bool   `json:"test"`
}
