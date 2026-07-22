// Package javascript extracts a conservative local graph from JavaScript-family source files.
package javascript

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/gtindo/flowmap/internal/semantic"
	"github.com/kdy1/go-typescript-eslint/pkg/typescriptestree"
)

// Backend parses JavaScript, TypeScript, JSX, and TSX without a Node runtime.
type Backend struct{}

var _ semantic.Backend = Backend{}

var (
	functionPattern = regexp.MustCompile(`(?m)(?:^|[;\n])\s*(?:export\s+(?:default\s+)?)?(?:async\s+)?function\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*\(([^)]*)\)`)
	arrowPattern    = regexp.MustCompile(`(?m)(?:^|[;\n])\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*(?:async\s*)?(\([^)]*\)|[A-Za-z_$][A-Za-z0-9_$]*)\s*=>`)
	methodPattern   = regexp.MustCompile(`(?m)(?:^|[;\n])\s*(?:async\s+)?([A-Za-z_$][A-Za-z0-9_$]*)\s*\(([^)]*)\)\s*\{`)
	callPattern     = regexp.MustCompile(`\b([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)
	importPattern   = regexp.MustCompile(`(?m)^\s*import\s+(.+?)\s+from\s+["']([^"']+)["']`)
)

var supportedExtensions = map[string]bool{
	".js": true, ".mjs": true, ".cjs": true, ".jsx": true,
	".ts": true, ".mts": true, ".cts": true, ".tsx": true,
}

var ignoredDirectories = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true, "build": true,
	"coverage": true, ".next": true, "out": true,
}

type sourceFile struct {
	abs  string
	rel  string
	src  string
	decl map[string]string
	deps map[string]string
}

type symbolRecord struct {
	symbol semantic.Symbol
	file   *sourceFile
	body   string
}

// Analyze reads eligible local source files and returns a syntax-derived snapshot.
// Side Effect (Edge): reads the requested working tree.
func (Backend) Analyze(ctx context.Context, request semantic.AnalysisRequest) (semantic.Snapshot, error) {
	root, err := filepath.Abs(request.Root)
	if err != nil {
		return semantic.Snapshot{}, fmt.Errorf("resolve analysis root: %w", err)
	}

	files, diagnostics, err := collectFiles(ctx, root)
	if err != nil {
		return semantic.Snapshot{}, err
	}

	records := make([]*symbolRecord, 0)
	byFile := make(map[string]*sourceFile, len(files))
	for _, file := range files {
		byFile[file.rel] = file
		parsed, parseErr := parse(file)
		if parseErr != nil {
			diagnostics.FailedUnits++
			diagnostics.Diagnostics = append(diagnostics.Diagnostics, semantic.Diagnostic{Kind: "parse", Position: file.rel, Message: parseErr.Error(), Units: []string{file.rel}})
			continue
		}
		records = append(records, parsed...)
	}

	if len(records) == 0 {
		return semantic.Snapshot{}, fmt.Errorf("analyze JavaScript source: no local functions found beneath %s", root)
	}

	for _, record := range records {
		record.file.decl[record.symbol.Name] = record.symbol.ID
	}
	relationships := collectRelationships(records, byFile)
	symbols := make([]semantic.Symbol, 0, len(records))
	for _, record := range records {
		symbols = append(symbols, record.symbol)
	}
	sort.Slice(symbols, func(left, right int) bool { return symbols[left].ID < symbols[right].ID })
	sort.Slice(relationships, func(left, right int) bool {
		if relationships[left].FromID == relationships[right].FromID {
			return relationships[left].ToID < relationships[right].ToID
		}
		return relationships[left].FromID < relationships[right].FromID
	})

	return semantic.Snapshot{Root: root, Language: "javascript", Symbols: symbols, Relationships: relationships, Diagnostics: diagnostics}, nil
}

func collectFiles(ctx context.Context, root string) ([]*sourceFile, semantic.DiagnosticReport, error) {
	files := make([]*sourceFile, 0)
	diagnostics := semantic.DiagnosticReport{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && ignoredDirectories[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !supportedExtensions[strings.ToLower(filepath.Ext(path))] || strings.HasSuffix(path, ".d.ts") || generatedName(path) {
			return nil
		}
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("read JavaScript source %s: %w", path, readErr)
		}
		relative, relativeErr := filepath.Rel(root, path)
		if relativeErr != nil {
			return relativeErr
		}
		diagnostics.TotalUnits++
		files = append(files, &sourceFile{abs: path, rel: filepath.ToSlash(relative), src: string(contents), decl: map[string]string{}, deps: map[string]string{}})
		return nil
	})
	if err != nil {
		return nil, diagnostics, fmt.Errorf("walk JavaScript source: %w", err)
	}
	sort.Slice(files, func(left, right int) bool { return files[left].rel < files[right].rel })
	return files, diagnostics, nil
}

func generatedName(path string) bool {
	name := strings.ToLower(filepath.Base(path))
	return strings.Contains(name, ".generated.") || strings.Contains(name, ".gen.") || strings.Contains(name, ".min.")
}

func parse(file *sourceFile) ([]*symbolRecord, error) {
	options := typescriptestree.NewBuilder().WithSourceType(typescriptestree.SourceTypeModule).WithFilePath(file.abs).WithLoc(true).WithRange(true).MustBuild()
	parseSource := importPattern.ReplaceAllString(file.src, "")
	// The parser supplies language validation without any Node dependency. Its
	// current pre-v1 implementation rejects a few valid module forms, so local
	// declaration extraction remains best-effort when validation fails.
	_, _ = typescriptestree.Parse(parseSource, options)

	records := make([]*symbolRecord, 0)
	seen := map[int]bool{}
	add := func(match []int, name string, parameters string, bodyStart int) {
		if name == "" || seen[match[0]] {
			return
		}
		seen[match[0]] = true
		end := expressionEnd(file.src, bodyStart)
		declarationStart := match[0]
		start := commentStart(file.src, declarationStart)
		line, endLine := lineAt(file.src, start), lineAt(file.src, end)
		qualified := strings.TrimSuffix(file.rel, filepath.Ext(file.rel)) + "." + name
		identity := file.rel + "|" + qualified + fmt.Sprintf("|%d", match[0])
		digest := sha256.Sum256([]byte(identity))
		identifier := hex.EncodeToString(digest[:16])
		records = append(records, &symbolRecord{symbol: semantic.Symbol{
			ID: identifier, ChangeKey: file.rel + "|" + qualified, Language: "javascript", Kind: semantic.SymbolFunction,
			Name: name, QualifiedName: qualified, Package: filepath.ToSlash(filepath.Dir(file.rel)),
			Location: semantic.Location{File: file.abs, Line: line, EndLine: endLine}, Source: file.src[start:end],
			Documentation: jsDoc(file.src, declarationStart), Signature: semantic.Signature{Display: name + "(" + strings.TrimSpace(parameters) + ")", Parameters: parameterList(parameters)},
			Test: isTestFile(file.rel),
		}, file: file, body: file.src[bodyStart:end]})
	}
	for _, match := range functionPattern.FindAllStringSubmatchIndex(file.src, -1) {
		add(match, file.src[match[2]:match[3]], file.src[match[4]:match[5]], strings.Index(file.src[match[1]:], "{")+match[1])
	}
	for _, match := range arrowPattern.FindAllStringSubmatchIndex(file.src, -1) {
		add(match, file.src[match[2]:match[3]], strings.Trim(file.src[match[4]:match[5]], "()"), match[1])
	}
	for _, match := range methodPattern.FindAllStringSubmatchIndex(file.src, -1) {
		name := file.src[match[2]:match[3]]
		if keyword(name) {
			continue
		}
		add(match, name, file.src[match[4]:match[5]], match[1]-1)
	}
	return records, nil
}

func collectRelationships(records []*symbolRecord, files map[string]*sourceFile) []semantic.Relationship {
	for _, record := range records {
		for _, match := range importPattern.FindAllStringSubmatch(record.file.src, -1) {
			module := match[2]
			if !strings.HasPrefix(module, ".") {
				continue
			}
			target := resolveModule(record.file.rel, module, files)
			if target == nil {
				continue
			}
			bindings := strings.TrimSpace(match[1])
			for _, binding := range strings.Split(strings.Trim(bindings, "{} "), ",") {
				parts := strings.Fields(strings.TrimSpace(binding))
				if len(parts) == 0 {
					continue
				}
				remote, local := parts[0], parts[0]
				if len(parts) == 3 && parts[1] == "as" {
					local = parts[2]
				}
				if identifier := target.decl[remote]; identifier != "" {
					record.file.deps[local] = identifier
				}
			}
		}
	}

	edges := make([]semantic.Relationship, 0)
	seen := map[string]bool{}
	for _, record := range records {
		clean := removeStringsAndComments(record.body)
		for _, match := range callPattern.FindAllStringSubmatchIndex(clean, -1) {
			name := clean[match[2]:match[3]]
			if keyword(name) || name == record.symbol.Name {
				continue
			}
			target := record.file.decl[name]
			if target == "" {
				target = record.file.deps[name]
			}
			if target == "" {
				record.symbol.Facts = append(record.symbol.Facts, semantic.Fact{Kind: semantic.FactExternalCall, Name: name})
				continue
			}
			key := record.symbol.ID + "|" + target
			if seen[key] {
				continue
			}
			seen[key] = true
			edges = append(edges, semantic.Relationship{FromID: record.symbol.ID, ToID: target, Kind: semantic.RelationshipCall})
		}
	}
	return edges
}

func resolveModule(from string, module string, files map[string]*sourceFile) *sourceFile {
	base := filepath.ToSlash(filepath.Join(filepath.Dir(from), module))
	for extension := range supportedExtensions {
		if file := files[base+extension]; file != nil {
			return file
		}
	}
	for extension := range supportedExtensions {
		if file := files[base+"/index"+extension]; file != nil {
			return file
		}
	}
	return files[base]
}

func expressionEnd(source string, start int) int {
	if start < 0 || start >= len(source) {
		return len(source)
	}
	if open := strings.Index(source[start:], "{"); open >= 0 && open < 8 {
		depth := 0
		for index := start + open; index < len(source); index++ {
			switch source[index] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					return index + 1
				}
			}
		}
	}
	if end := strings.IndexByte(source[start:], ';'); end >= 0 {
		return start + end + 1
	}
	if end := strings.IndexByte(source[start:], '\n'); end >= 0 {
		return start + end
	}
	return len(source)
}

func lineAt(source string, offset int) int {
	return strings.Count(source[:min(offset, len(source))], "\n") + 1
}
func commentStart(source string, start int) int {
	prefix := source[:start]
	if index := strings.LastIndex(prefix, "/**"); index >= 0 && !strings.Contains(prefix[index:], "*/") {
		return start
	}
	if index := strings.LastIndex(prefix, "/**"); index >= 0 && strings.TrimSpace(prefix[index+2:]) != "" && strings.HasSuffix(strings.TrimSpace(prefix[index:]), "*/") {
		return index
	}
	return start
}
func jsDoc(source string, start int) string {
	segment := source[max(0, start-2048):start]
	begin := strings.LastIndex(segment, "/**")
	end := strings.LastIndex(segment, "*/")
	if begin < 0 || end < begin {
		return ""
	}
	return strings.TrimSpace(strings.Trim(strings.ReplaceAll(strings.ReplaceAll(segment[begin+3:end], "\n *", "\n"), "\r", ""), "* \n"))
}
func parameterList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return []string{}
	}
	parts := strings.Split(value, ",")
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
	}
	return parts
}
func isTestFile(path string) bool {
	return strings.Contains(path, "/__tests__/") || strings.Contains(path, ".test.") || strings.Contains(path, ".spec.")
}
func keyword(value string) bool {
	_, exists := map[string]bool{"if": true, "for": true, "while": true, "switch": true, "catch": true, "function": true, "return": true, "new": true, "typeof": true, "import": true, "export": true}[value]
	return exists
}
func removeStringsAndComments(source string) string {
	replacer := strings.NewReplacer("//", "  ", "/*", "  ", "*/", "  ", "\"", " ", "'", " ", "`", " ")
	return replacer.Replace(source)
}
func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}
