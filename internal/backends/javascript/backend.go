// Package javascript extracts a conservative local graph from JavaScript-family source files.
package javascript

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gtindo/flowmap/internal/semantic"
)

// Backend parses JavaScript, TypeScript, JSX, and TSX without a Node runtime.
type Backend struct{}

var _ semantic.Backend = Backend{}

var (
	functionPattern       = regexp.MustCompile(`\b(?:export\s+(?:default\s+)?)?(?:async\s+)?function\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*\(([^)]*)\)\s*(?::\s*([^={]+))?\s*\{`)
	arrowPattern          = regexp.MustCompile(`\b(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*(?::[^=;]+)?=\s*(?:async\s*)?(\([^)]*\)|[A-Za-z_$][A-Za-z0-9_$]*)\s*=>`)
	classPattern          = regexp.MustCompile(`\b(?:export\s+(?:default\s+)?)?class\s+([A-Za-z_$][A-Za-z0-9_$]*)(?:\s+extends\s+([A-Za-z_$][A-Za-z0-9_$]*))?[^\{]*\{`)
	classExpression       = regexp.MustCompile(`\b(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*(?::[^=;]+)?=\s*class(?:\s+[A-Za-z_$][A-Za-z0-9_$]*)?(?:\s+extends\s+([A-Za-z_$][A-Za-z0-9_$]*))?[^\{]*\{`)
	methodPattern         = regexp.MustCompile(`(?m)(?:^|[;{}])\s*(?:(?:public|private|protected|readonly|abstract|declare|override)\s+)*(?:(static)\s+)?(?:(async)\s+)?(?:(get|set)\s+)?(\*?)\s*(constructor|[A-Za-z_$][A-Za-z0-9_$]*)\s*\(([^)]*)\)\s*(?::\s*[^\{=]+)?\s*\{`)
	accessModifierPattern = regexp.MustCompile(`\b(private|protected|public)\b`)
	directCallPattern     = regexp.MustCompile(`\b([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)
	memberCallPattern     = regexp.MustCompile(`\b((?:this|super|[A-Za-z_$][A-Za-z0-9_$]*)(?:\.[A-Za-z_$][A-Za-z0-9_$]*)*)\.([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)
	importPattern         = regexp.MustCompile(`(?m)^\s*import\s+(.+?)\s+from\s+["']([^"']+)["']`)
	requirePattern        = regexp.MustCompile(`\b(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*require\(\s*["']([^"']+)["']\s*\)`)
	exportNamedPattern    = regexp.MustCompile(`(?m)^\s*export\s*\{([^}]+)\}(?:\s*from\s*["']([^"']+)["'])?`)
	exportStarPattern     = regexp.MustCompile(`(?m)^\s*export\s*\*\s*from\s*["']([^"']+)["']`)
	cjsExportPattern      = regexp.MustCompile(`\b(?:exports|module\.exports)\.([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*([A-Za-z_$][A-Za-z0-9_$]*)`)
	cjsObjectPattern      = regexp.MustCompile(`\bmodule\.exports\s*=\s*\{([^}]*)\}`)
	newBindingPattern     = regexp.MustCompile(`\b(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*new\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)
	typedBindingPattern   = regexp.MustCompile(`\b(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*[!?]?\s*:\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*(?:[;=])`)
	fieldTypePattern      = regexp.MustCompile(`(?m)^\s*(?:(?:public|private|protected|readonly|declare|override)\s+)*([A-Za-z_$][A-Za-z0-9_$]*)\s*[!?]?\s*:\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*(?:;|=)`)
	aliasBindingPattern   = regexp.MustCompile(`\b(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*(?:;|\n)`)
	factoryBindingPattern = regexp.MustCompile(`\b(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)
)

// moduleExtensions lists supported extensions in resolution priority order so
// that an import matching both `name.ts` and `name.js` resolves deterministically.
var moduleExtensions = []string{".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs"}

var supportedExtensions = map[string]bool{
	".js": true, ".mjs": true, ".cjs": true, ".jsx": true,
	".ts": true, ".mts": true, ".cts": true, ".tsx": true,
}

// ignoredDirectories covers dependency stores, caches, and build outputs that
// are commonly present even when a repository's ignore rules are unavailable.
var ignoredDirectories = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true, "build": true,
	"coverage": true, ".next": true, "out": true, "bower_components": true, "jspm_packages": true,
	".yarn": true, ".pnpm-store": true, ".turbo": true, ".cache": true, ".parcel-cache": true,
	".nuxt": true, ".output": true, ".svelte-kit": true, ".vercel": true, ".netlify": true,
	".docusaurus": true, ".expo": true, ".angular": true, "storybook-static": true,
}

// ignoredFiles are package-manager runtime files that are large generated code.
var ignoredFiles = map[string]bool{
	".pnp.cjs": true, ".pnp.js": true, ".pnp.loader.mjs": true,
}

var keywords = map[string]bool{
	"if": true, "for": true, "while": true, "switch": true, "catch": true, "function": true, "return": true,
	"new": true, "typeof": true, "import": true, "export": true, "class": true,
}

const (
	// maxSourceBytes bounds a single file; larger JavaScript is almost always
	// bundled or generated output that adds noise rather than local intent.
	maxSourceBytes = 1 << 20
	// minifiedMinimumBytes avoids flagging short one-line modules as minified.
	minifiedMinimumBytes = 16 << 10
	// minifiedAverageLineBytes is the average line length that marks minified output.
	minifiedAverageLineBytes = 250
	// arrowBodyBraceWindow is how far after `=>` a block body brace may start.
	arrowBodyBraceWindow  = 8
	skippedDiagnosticKind = "skipped"
)

type sourceFile struct {
	abs string
	rel string
	src string

	// masked, lineStarts, semicolons, and braces are computed once per file so
	// per-symbol lookups never rescan the whole source.
	masked     string
	lineStarts []int
	semicolons []int
	braces     map[int]int

	// Module syntax matches are scanned during the parallel parse phase and
	// applied later by the order-sensitive, serial linking phase.
	importMatches      [][]string
	requireMatches     [][]string
	exportNamedMatches [][]string
	exportStarMatches  [][]string

	functions       map[string]*symbolRecord
	classes         map[string]*classInfo
	exportFunctions map[string]*symbolRecord
	exportClasses   map[string]*classInfo
	dependencies    map[string]*symbolRecord
	classAliases    map[string]*classInfo
	moduleAliases   map[string]*sourceFile
	reexports       []reexport
}

type reexport struct {
	module string
	remote string
	local  string
	all    bool
}

type classInfo struct {
	name        string
	file        *sourceFile
	start       int
	end         int
	extendsName string
	parent      *classInfo
	methods     map[string]*symbolRecord
	statics     map[string]*symbolRecord
	fields      map[string]string
}

type symbolRecord struct {
	symbol semantic.Symbol
	file   *sourceFile
	body   string

	maskedBody string

	class      *classInfo
	memberName string
	access     string
	static     bool
	returnType string
	exported   bool
}

// Analyze reads eligible local source files and returns a syntax-derived snapshot.
// Side Effect (Edge): reads the requested working tree.
func (Backend) Analyze(ctx context.Context, request semantic.AnalysisRequest) (semantic.Snapshot, error) {
	root, err := filepath.Abs(request.Root)
	if err != nil {
		return semantic.Snapshot{}, fmt.Errorf("resolve analysis root: %w", err)
	}

	paths, err := sourcePaths(ctx, root)
	if err != nil {
		return semantic.Snapshot{}, err
	}

	files, records, diagnostics, err := loadAndParse(ctx, root, paths)
	if err != nil {
		return semantic.Snapshot{}, err
	}
	if len(records) == 0 {
		return semantic.Snapshot{}, fmt.Errorf("analyze JavaScript source: no local functions found beneath %s", root)
	}

	byFile := make(map[string]*sourceFile, len(files))
	for _, file := range files {
		byFile[file.rel] = file
	}
	linkModules(files, byFile)
	markPublicCallables(records, files)

	relationships, err := collectRelationships(ctx, records)
	if err != nil {
		return semantic.Snapshot{}, err
	}

	symbols := make([]semantic.Symbol, 0, len(records))
	for _, record := range records {
		symbols = append(symbols, record.symbol)
	}
	sort.Slice(symbols, func(left, right int) bool { return symbols[left].ID < symbols[right].ID })
	sort.Slice(relationships, func(left, right int) bool {
		if relationships[left].FromID != relationships[right].FromID {
			return relationships[left].FromID < relationships[right].FromID
		}
		if relationships[left].ToID != relationships[right].ToID {
			return relationships[left].ToID < relationships[right].ToID
		}
		return relationships[left].Kind < relationships[right].Kind
	})

	return semantic.Snapshot{Root: root, Language: "javascript", Symbols: symbols, Relationships: relationships, Diagnostics: diagnostics}, nil
}

// fileOutcome is one file's independent load and parse result.
type fileOutcome struct {
	file    *sourceFile
	records []*symbolRecord
	skip    string
	failure *semantic.Diagnostic
}

// loadAndParse reads and parses candidate files concurrently, then folds the
// outcomes in path order so the result is deterministic.
// Side Effect (Edge): reads source files.
func loadAndParse(ctx context.Context, root string, paths []string) ([]*sourceFile, []*symbolRecord, semantic.DiagnosticReport, error) {
	outcomes := make([]fileOutcome, len(paths))
	err := forEachParallel(ctx, len(paths), func(index int) {
		outcomes[index] = loadAndParseFile(root, paths[index])
	})
	if err != nil {
		return nil, nil, semantic.DiagnosticReport{}, err
	}

	files := make([]*sourceFile, 0, len(paths))
	records := make([]*symbolRecord, 0)
	diagnostics := semantic.DiagnosticReport{}
	skippedByReason := map[string][]string{}
	for _, outcome := range outcomes {
		if outcome.skip != "" {
			skippedByReason[outcome.skip] = append(skippedByReason[outcome.skip], outcome.file.rel)
			continue
		}
		if outcome.file == nil {
			continue
		}

		diagnostics.TotalUnits++
		files = append(files, outcome.file)
		if outcome.failure != nil {
			diagnostics.FailedUnits++
			diagnostics.Diagnostics = append(diagnostics.Diagnostics, *outcome.failure)
			continue
		}
		records = append(records, outcome.records...)
	}

	reasons := make([]string, 0, len(skippedByReason))
	for reason := range skippedByReason {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	for _, reason := range reasons {
		units := skippedByReason[reason]
		diagnostics.Diagnostics = append(diagnostics.Diagnostics, semantic.Diagnostic{Kind: skippedDiagnosticKind, Position: units[0], Message: fmt.Sprintf("skipped %d %s file(s)", len(units), reason), Units: units})
	}
	return files, records, diagnostics, nil
}

// loadAndParseFile reads one file, applies size guards, and extracts callables.
// Side Effect (Edge): reads one source file.
func loadAndParseFile(root, relative string) fileOutcome {
	path := filepath.Join(root, filepath.FromSlash(relative))
	contents, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		// Git can list tracked files that were deleted from the working tree.
		return fileOutcome{}
	}

	file := newSourceFile(path, relative, string(contents))
	if err != nil {
		return fileOutcome{file: file, failure: &semantic.Diagnostic{Kind: "read", Position: relative, Message: err.Error(), Units: []string{relative}}}
	}
	if len(contents) > maxSourceBytes {
		return fileOutcome{file: file, skip: "oversized"}
	}
	if looksMinified(contents) {
		return fileOutcome{file: file, skip: "minified"}
	}

	records, err := parse(file)
	if err != nil {
		return fileOutcome{file: file, failure: &semantic.Diagnostic{Kind: "parse", Position: relative, Message: err.Error(), Units: []string{relative}}}
	}
	return fileOutcome{file: file, records: records}
}

// looksMinified detects bundled or minified output by its average line length.
// Operations (Pure): inspects explicit file contents.
func looksMinified(contents []byte) bool {
	if len(contents) < minifiedMinimumBytes {
		return false
	}

	lines := bytes.Count(contents, []byte{'\n'}) + 1
	return len(contents)/lines >= minifiedAverageLineBytes
}

func newSourceFile(abs, rel, src string) *sourceFile {
	return &sourceFile{abs: abs, rel: rel, src: src, functions: map[string]*symbolRecord{}, classes: map[string]*classInfo{}, exportFunctions: map[string]*symbolRecord{}, exportClasses: map[string]*classInfo{}, dependencies: map[string]*symbolRecord{}, classAliases: map[string]*classInfo{}, moduleAliases: map[string]*sourceFile{}}
}

// sourcePaths lists candidate files relative to root. Inside a Git work tree it
// honors the repository's ignore rules; otherwise it walks the directory.
// Side Effect (Edge): runs Git or walks the filesystem.
func sourcePaths(ctx context.Context, root string) ([]string, error) {
	if listed, ok := gitSourcePaths(ctx, root); ok {
		if paths := filterSourcePaths(listed); len(paths) > 0 {
			return paths, nil
		}
	}
	return walkSourcePaths(ctx, root)
}

// gitSourcePaths returns tracked and untracked-but-not-ignored files beneath root.
// Side Effect (Edge): runs Git.
func gitSourcePaths(ctx context.Context, root string) ([]string, bool) {
	command := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	output, err := command.Output()
	if err != nil {
		return nil, false
	}

	paths := make([]string, 0)
	for _, entry := range bytes.Split(output, []byte{0}) {
		if len(entry) > 0 {
			paths = append(paths, string(entry))
		}
	}
	return paths, true
}

// walkSourcePaths lists candidate files when Git ignore rules are unavailable.
// Side Effect (Edge): walks the filesystem.
func walkSourcePaths(ctx context.Context, root string) ([]string, error) {
	paths := make([]string, 0)
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

		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk JavaScript source: %w", err)
	}
	return filterSourcePaths(paths), nil
}

// filterSourcePaths keeps supported, non-generated sources outside ignored
// directories, sorted and deduplicated.
// Operations (Pure): filters explicit relative paths.
func filterSourcePaths(paths []string) []string {
	result := make([]string, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		if seen[path] || !isCandidateSource(path) {
			continue
		}
		seen[path] = true
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

func isCandidateSource(relative string) bool {
	segments := strings.Split(relative, "/")
	for _, directory := range segments[:len(segments)-1] {
		if ignoredDirectories[directory] {
			return false
		}
	}

	name := segments[len(segments)-1]
	if ignoredFiles[name] || strings.HasSuffix(name, ".d.ts") || generatedName(name) {
		return false
	}
	return supportedExtensions[strings.ToLower(filepath.Ext(name))]
}

func generatedName(name string) bool {
	name = strings.ToLower(name)
	return strings.Contains(name, ".generated.") || strings.Contains(name, ".gen.") || strings.Contains(name, ".min.")
}

// forEachParallel runs work for every index on a bounded worker pool. A panic
// in a worker is re-raised on the calling goroutine so callers keep their
// usual recovery behavior.
func forEachParallel(ctx context.Context, count int, work func(index int)) error {
	workers := min(runtime.GOMAXPROCS(0), count)
	var next atomic.Int64
	var panicOnce sync.Once
	var recovered any

	var group sync.WaitGroup
	for range workers {
		group.Go(func() {
			defer func() {
				if value := recover(); value != nil {
					panicOnce.Do(func() { recovered = value })
				}
			}()
			for ctx.Err() == nil {
				index := int(next.Add(1)) - 1
				if index >= count {
					return
				}
				work(index)
			}
		})
	}
	group.Wait()

	if recovered != nil {
		panic(recovered)
	}
	return ctx.Err()
}

// DeclarationNames returns callable names for JavaScript-family source. It is
// used by Git attribution so class methods use the same owner-qualified keys as
// the semantic snapshot. The path selects syntax handling, such as whether JSX
// may appear.
// Operations (Pure): extracts callable names from explicit source text.
func DeclarationNames(path, source string) []string {
	file := newSourceFile("", filepath.ToSlash(path), source)
	records, _ := parse(file)
	names := make([]string, 0, len(records))
	for _, record := range records {
		names = append(names, record.symbol.Name)
	}
	return names
}

// parse extracts callable records from one file. Extraction is best effort:
// unexpected source shapes become a per-file error instead of failing the scan.
// Operations (Pure): reads and annotates the explicit file model.
func parse(file *sourceFile) (records []*symbolRecord, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			records, err = nil, fmt.Errorf("extract callables: %v", recovered)
		}
	}()

	prepareSource(file)
	scanModuleSyntax(file)
	masked := file.masked
	classes := extractClasses(file, masked)
	records = make([]*symbolRecord, 0)
	for _, class := range classes {
		records = append(records, extractMethods(file, class, masked)...)
	}
	for _, match := range functionPattern.FindAllStringSubmatchIndex(masked, -1) {
		if insideClass(match[0], classes) {
			continue
		}
		name := file.src[match[2]:match[3]]
		parameters := file.src[match[4]:match[5]]
		returnType := ""
		if match[6] >= 0 {
			returnType = strings.TrimSpace(file.src[match[6]:match[7]])
		}
		records = append(records, addRecord(file, nil, name, name, parameters, match[0], match[1]-1, returnType, false, exportedAt(file.src, match[0])))
	}
	for _, match := range arrowPattern.FindAllStringSubmatchIndex(masked, -1) {
		if insideClass(match[0], classes) {
			continue
		}
		name := file.src[match[2]:match[3]]
		parameters := strings.Trim(file.src[match[4]:match[5]], "()")
		records = append(records, addRecord(file, nil, name, name, parameters, match[0], match[1], "", false, exportedAt(file.src, match[0])))
	}
	for _, match := range cjsExportPattern.FindAllStringSubmatch(file.src, -1) {
		if record := file.functions[match[2]]; record != nil {
			file.exportFunctions[match[1]] = record
		}
		if class := file.classes[match[2]]; class != nil {
			file.exportClasses[match[1]] = class
		}
	}
	for _, match := range cjsObjectPattern.FindAllStringSubmatch(file.src, -1) {
		for _, binding := range strings.Split(match[1], ",") {
			parts := strings.Split(strings.TrimSpace(binding), ":")
			local := strings.TrimSpace(parts[0])
			remote := local
			if len(parts) == 2 {
				remote = strings.TrimSpace(parts[0])
				local = strings.TrimSpace(parts[1])
			}
			if record := file.functions[local]; record != nil {
				file.exportFunctions[remote] = record
			}
			if class := file.classes[local]; class != nil {
				file.exportClasses[remote] = class
			}
		}
	}
	return records, nil
}

func extractClasses(file *sourceFile, masked string) []*classInfo {
	classes := make([]*classInfo, 0)
	add := func(match []int, name, parent string, brace int, exported bool) {
		if name == "" || file.classes[name] != nil {
			return
		}
		end := file.matchingBrace(brace)
		if end < 0 {
			return
		}
		class := &classInfo{name: name, file: file, start: match[0], end: end + 1, extendsName: parent, methods: map[string]*symbolRecord{}, statics: map[string]*symbolRecord{}, fields: map[string]string{}}
		file.classes[name] = class
		file.classAliases[name] = class
		if exported {
			file.exportClasses[name] = class
			if defaultExportAt(file.src, match[0]) {
				file.exportClasses["default"] = class
			}
		}
		classes = append(classes, class)
	}
	for _, match := range classPattern.FindAllStringSubmatchIndex(masked, -1) {
		parent := ""
		if match[4] >= 0 {
			parent = file.src[match[4]:match[5]]
		}
		brace := strings.LastIndex(masked[match[0]:match[1]], "{") + match[0]
		add(match, file.src[match[2]:match[3]], parent, brace, exportedAt(file.src, match[0]))
	}
	for _, match := range classExpression.FindAllStringSubmatchIndex(masked, -1) {
		parent := ""
		if match[4] >= 0 {
			parent = file.src[match[4]:match[5]]
		}
		brace := strings.LastIndex(masked[match[0]:match[1]], "{") + match[0]
		add(match, file.src[match[2]:match[3]], parent, brace, exportedAt(file.src, match[0]))
	}
	return classes
}

func extractMethods(file *sourceFile, class *classInfo, masked string) []*symbolRecord {
	records := make([]*symbolRecord, 0)
	bodyStart := strings.Index(masked[class.start:class.end], "{") + class.start + 1
	body := masked[bodyStart : class.end-1]
	depth := braceCursor{text: body}
	for _, match := range methodPattern.FindAllStringSubmatchIndex(body, -1) {
		start := bodyStart + match[0]
		name := file.src[bodyStart+match[10] : bodyStart+match[11]]
		parameters := file.src[bodyStart+match[12] : bodyStart+match[13]]
		brace := bodyStart + match[1] - 1
		for brace >= start && masked[brace] != '{' {
			brace--
		}
		if brace < start {
			continue
		}
		if depth.at(brace-bodyStart) != 0 {
			continue
		}
		memberName := name
		if name == "constructor" {
			memberName = "constructor"
		}
		record := addRecord(file, class, class.name+"."+memberName, memberName, parameters, start, brace, "", match[2] >= 0, true)
		record.access = methodAccess(file.src[start:brace])
		records = append(records, record)
		if match[2] >= 0 {
			class.statics[memberName] = record
		} else if match[6] < 0 {
			class.methods[memberName] = record
		}
	}
	extractClassFields(class, body)
	return records
}

func markPublicCallables(records []*symbolRecord, files []*sourceFile) {
	exportedClasses := make(map[*classInfo]bool)
	for _, file := range files {
		for _, record := range file.exportFunctions {
			record.symbol.Public = true
		}
		for _, class := range file.exportClasses {
			exportedClasses[class] = true
		}
	}

	for _, record := range records {
		if record.class == nil {
			record.symbol.Public = record.symbol.Public || record.exported
			continue
		}
		if exportedClasses[record.class] && record.access != "private" && record.access != "protected" {
			record.symbol.Public = true
		}
	}
}

func methodAccess(declaration string) string {
	match := accessModifierPattern.FindStringSubmatch(declaration)
	if len(match) == 2 {
		return match[1]
	}
	return "public"
}

func extractClassFields(class *classInfo, body string) {
	depth := braceCursor{text: body}
	for _, match := range fieldTypePattern.FindAllStringSubmatchIndex(body, -1) {
		if depth.at(match[0]) != 0 {
			continue
		}
		class.fields[body[match[2]:match[3]]] = body[match[4]:match[5]]
	}
}

func addRecord(file *sourceFile, class *classInfo, name, memberName, parameters string, declarationStart, bodyStart int, returnType string, static, exported bool) *symbolRecord {
	end := expressionEnd(file, bodyStart)
	start := commentStart(file.src, declarationStart)
	line, endLine := file.lineAt(start), file.lineAt(end)
	qualified := strings.TrimSuffix(file.rel, filepath.Ext(file.rel)) + "." + name
	identity := file.rel + "|" + qualified + fmt.Sprintf("|%d", declarationStart)
	digest := sha256.Sum256([]byte(identity))
	identifier := hex.EncodeToString(digest[:16])
	kind := semantic.SymbolFunction
	if class != nil {
		kind = semantic.SymbolMethod
	}
	record := &symbolRecord{symbol: semantic.Symbol{ID: identifier, ChangeKey: file.rel + "|" + qualified, Language: "javascript", Kind: kind, Name: name, QualifiedName: qualified, Package: filepath.ToSlash(filepath.Dir(file.rel)), Location: semantic.Location{File: file.abs, Line: line, EndLine: endLine}, Source: file.src[start:end], Documentation: jsDoc(file.src, declarationStart), Signature: semantic.Signature{Display: name + "(" + strings.TrimSpace(parameters) + ")", Parameters: parameterList(parameters)}, Test: isTestFile(file.rel)}, file: file, body: file.src[bodyStart:end], maskedBody: file.masked[bodyStart:end], class: class, memberName: memberName, static: static, returnType: simpleType(returnType), exported: exported}
	if class == nil {
		file.functions[name] = record
		if exported {
			file.exportFunctions[name] = record
		}
	}
	return record
}

func linkModules(files []*sourceFile, byFile map[string]*sourceFile) {
	for _, file := range files {
		linkImports(file, byFile)
	}
	for iteration := 0; iteration < len(files); iteration++ {
		changed := false
		for _, file := range files {
			for _, item := range file.reexports {
				target := resolveModule(file.rel, item.module, byFile)
				if target == nil {
					continue
				}
				if item.all {
					for name, record := range target.exportFunctions {
						if file.exportFunctions[name] == nil {
							file.exportFunctions[name] = record
							changed = true
						}
					}
					for name, class := range target.exportClasses {
						if file.exportClasses[name] == nil {
							file.exportClasses[name] = class
							changed = true
						}
					}
					continue
				}
				if record := target.exportFunctions[item.remote]; record != nil && file.exportFunctions[item.local] == nil {
					file.exportFunctions[item.local] = record
					changed = true
				}
				if class := target.exportClasses[item.remote]; class != nil && file.exportClasses[item.local] == nil {
					file.exportClasses[item.local] = class
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}
	for _, file := range files {
		resolveClassLinks(file)
	}
}

// scanModuleSyntax records import, require, and export matches for linking.
// Operations (Pure): scans explicit source text.
func scanModuleSyntax(file *sourceFile) {
	file.importMatches = importPattern.FindAllStringSubmatch(file.src, -1)
	file.requireMatches = requirePattern.FindAllStringSubmatch(file.src, -1)
	file.exportNamedMatches = exportNamedPattern.FindAllStringSubmatch(file.src, -1)
	file.exportStarMatches = exportStarPattern.FindAllStringSubmatch(file.src, -1)
}

func linkImports(file *sourceFile, files map[string]*sourceFile) {
	for _, match := range file.importMatches {
		if !strings.HasPrefix(match[2], ".") {
			continue
		}
		target := resolveModule(file.rel, match[2], files)
		if target == nil {
			continue
		}
		bindings := strings.TrimSpace(match[1])
		if strings.HasPrefix(bindings, "*") {
			parts := strings.Fields(bindings)
			if len(parts) == 3 && parts[1] == "as" {
				file.moduleAliases[parts[2]] = target
			}
			continue
		}
		if !strings.HasPrefix(bindings, "{") {
			defaultName := strings.TrimSpace(strings.Split(bindings, ",")[0])
			if class := target.exportClasses["default"]; class != nil {
				file.classAliases[defaultName] = class
			}
			if record := target.exportFunctions["default"]; record != nil {
				file.dependencies[defaultName] = record
			}
		}
		open, close := strings.Index(bindings, "{"), strings.Index(bindings, "}")
		if open < 0 || close < open {
			continue
		}
		for _, binding := range strings.Split(bindings[open+1:close], ",") {
			parts := strings.Fields(strings.TrimSpace(binding))
			if len(parts) == 0 {
				continue
			}
			remote, local := parts[0], parts[0]
			if len(parts) == 3 && parts[1] == "as" {
				local = parts[2]
			}
			if record := target.exportFunctions[remote]; record != nil {
				file.dependencies[local] = record
			}
			if class := target.exportClasses[remote]; class != nil {
				file.classAliases[local] = class
			}
		}
	}
	for _, match := range file.requireMatches {
		if strings.HasPrefix(match[2], ".") {
			if target := resolveModule(file.rel, match[2], files); target != nil {
				file.moduleAliases[match[1]] = target
			}
		}
	}
	for _, match := range file.exportNamedMatches {
		module := ""
		if len(match) > 2 {
			module = match[2]
		}
		for _, binding := range strings.Split(match[1], ",") {
			parts := strings.Fields(strings.TrimSpace(binding))
			if len(parts) == 0 {
				continue
			}
			remote, local := parts[0], parts[0]
			if len(parts) == 3 && parts[1] == "as" {
				local = parts[2]
			}
			if module == "" {
				if record := file.functions[remote]; record != nil {
					file.exportFunctions[local] = record
				}
				if class := file.classes[remote]; class != nil {
					file.exportClasses[local] = class
				}
			} else if strings.HasPrefix(module, ".") {
				file.reexports = append(file.reexports, reexport{module: module, remote: remote, local: local})
			}
		}
	}
	for _, match := range file.exportStarMatches {
		if strings.HasPrefix(match[1], ".") {
			file.reexports = append(file.reexports, reexport{module: match[1], all: true})
		}
	}
}

func resolveClassLinks(file *sourceFile) {
	for _, class := range file.classes {
		if class.extendsName != "" {
			class.parent = file.classAliases[class.extendsName]
		}
	}
}

// collectRelationships resolves each record's calls concurrently and merges
// the per-record edges in record order with global de-duplication.
func collectRelationships(ctx context.Context, records []*symbolRecord) ([]semantic.Relationship, error) {
	perRecord := make([][]semantic.Relationship, len(records))
	err := forEachParallel(ctx, len(records), func(index int) {
		perRecord[index] = recordRelationships(records[index])
	})
	if err != nil {
		return nil, err
	}

	edges := make([]semantic.Relationship, 0)
	seen := map[semantic.Relationship]bool{}
	for _, relationships := range perRecord {
		for _, relationship := range relationships {
			key := semantic.Relationship{FromID: relationship.FromID, ToID: relationship.ToID, Kind: relationship.Kind}
			if seen[key] {
				continue
			}
			seen[key] = true
			edges = append(edges, relationship)
		}
	}
	return edges, nil
}

// recordRelationships finds one record's local call and dependency edges and
// appends its external-call facts. It only mutates the given record.
func recordRelationships(record *symbolRecord) []semantic.Relationship {
	edges := make([]semantic.Relationship, 0)
	add := func(target *symbolRecord, kind string, dynamic bool) {
		if target == nil || target == record {
			return
		}
		edges = append(edges, semantic.Relationship{FromID: record.symbol.ID, ToID: target.symbol.ID, Kind: kind, Dynamic: dynamic})
	}

	clean := record.maskedBody
	bindings := receiverBindings(record)
	for _, match := range memberCallPattern.FindAllStringSubmatch(clean, -1) {
		receiver, method := match[1], match[2]
		if target, direct := resolveMember(record, receiver, method, bindings); target != nil {
			kind := semantic.RelationshipCall
			if !direct {
				kind = semantic.RelationshipDependency
			}
			add(target, kind, !direct)
		}
	}
	for _, match := range directCallPattern.FindAllStringSubmatchIndex(clean, -1) {
		name := clean[match[2]:match[3]]
		if keywords[name] || name == record.memberName || precededByDot(clean, match[0]) {
			continue
		}
		if target := record.file.functions[name]; target != nil {
			add(target, semantic.RelationshipCall, false)
			continue
		}
		if target := record.file.dependencies[name]; target != nil {
			add(target, semantic.RelationshipCall, false)
			continue
		}
		if !knownNonCall(name) {
			record.symbol.Facts = append(record.symbol.Facts, semantic.Fact{Kind: semantic.FactExternalCall, Name: name})
		}
	}
	return edges
}

func resolveMember(record *symbolRecord, receiver, method string, bindings map[string]*classInfo) (*symbolRecord, bool) {
	if receiver == "this" && record.class != nil {
		return record.class.methods[method], true
	}
	if receiver == "super" && record.class != nil && record.class.parent != nil {
		return record.class.parent.methods[method], true
	}
	if module := record.file.moduleAliases[receiver]; module != nil {
		return module.exportFunctions[method], true
	}
	if class := record.file.classAliases[receiver]; class != nil {
		return class.statics[method], true
	}
	if class := bindings[receiver]; class != nil {
		return class.methods[method], false
	}
	if strings.HasPrefix(receiver, "this.") && record.class != nil {
		if class := record.file.classAliases[record.class.fields[strings.TrimPrefix(receiver, "this.")]]; class != nil {
			return class.methods[method], false
		}
	}
	return nil, false
}

func receiverBindings(record *symbolRecord) map[string]*classInfo {
	bindings := map[string]*classInfo{}
	for _, parameter := range record.symbol.Signature.Parameters {
		parts := strings.Split(parameter, ":")
		if len(parts) != 2 {
			continue
		}
		name, typeName := strings.TrimSpace(strings.TrimSuffix(parts[0], "?")), strings.TrimSpace(parts[1])
		if !isSimpleType(typeName) {
			continue
		}
		if class := record.file.classAliases[typeName]; class != nil {
			bindings[name] = class
		}
	}
	clean := record.maskedBody
	for _, match := range typedBindingPattern.FindAllStringSubmatch(clean, -1) {
		if class := record.file.classAliases[match[2]]; class != nil {
			bindings[match[1]] = class
		}
	}
	for _, match := range newBindingPattern.FindAllStringSubmatch(clean, -1) {
		if class := record.file.classAliases[match[2]]; class != nil {
			bindings[match[1]] = class
		}
	}
	for _, match := range factoryBindingPattern.FindAllStringSubmatch(clean, -1) {
		factory := record.file.functions[match[2]]
		if factory == nil {
			factory = record.file.dependencies[match[2]]
		}
		if factory != nil {
			if class := record.file.classAliases[factory.returnType]; class != nil {
				bindings[match[1]] = class
			}
		}
	}
	for iteration := 0; iteration < 2; iteration++ {
		for _, match := range aliasBindingPattern.FindAllStringSubmatch(clean, -1) {
			if class := bindings[match[2]]; class != nil {
				bindings[match[1]] = class
			}
		}
	}
	return bindings
}

func resolveModule(from, module string, files map[string]*sourceFile) *sourceFile {
	base := filepath.ToSlash(filepath.Join(filepath.Dir(from), module))
	for _, extension := range moduleExtensions {
		if file := files[base+extension]; file != nil {
			return file
		}
	}
	for _, extension := range moduleExtensions {
		if file := files[base+"/index"+extension]; file != nil {
			return file
		}
	}
	return files[base]
}

// expressionEnd finds where a callable body ends: its matching block brace
// when the body opens with one, otherwise the next semicolon or line break.
func expressionEnd(file *sourceFile, start int) int {
	source := file.src
	if start < 0 || start >= len(source) {
		return len(source)
	}

	window := source[start:min(len(source), start+arrowBodyBraceWindow)]
	if open := strings.IndexByte(window, '{'); open >= 0 {
		if end := file.matchingBrace(start + open); end >= 0 {
			return end + 1
		}
	}
	if end := firstAtOrAfter(file.semicolons, start); end >= 0 {
		return end + 1
	}
	if line := firstAtOrAfter(file.lineStarts, start+1); line >= 0 {
		return line - 1
	}
	return len(source)
}

// prepareSource computes the per-file indexes that keep extraction linear.
// Operations (Pure): derives indexes from explicit source text.
func prepareSource(file *sourceFile) {
	if file.braces != nil {
		return
	}

	file.masked = maskSource(file.src, jsxCapable(file.rel))
	file.lineStarts = []int{0}
	file.semicolons = make([]int, 0)
	for index := 0; index < len(file.src); index++ {
		switch file.src[index] {
		case '\n':
			file.lineStarts = append(file.lineStarts, index+1)
		case ';':
			file.semicolons = append(file.semicolons, index)
		}
	}
	file.braces = matchBraces(file.masked)
}

// matchBraces pairs every balanced brace in one pass.
// Operations (Pure): indexes explicit masked source.
func matchBraces(masked string) map[int]int {
	pairs := make(map[int]int)
	open := make([]int, 0)
	for index := 0; index < len(masked); index++ {
		switch masked[index] {
		case '{':
			open = append(open, index)
		case '}':
			if len(open) == 0 {
				continue
			}
			pairs[open[len(open)-1]] = index
			open = open[:len(open)-1]
		}
	}
	return pairs
}

// matchingBrace returns the index of the brace closing the one at open, or -1.
func (file *sourceFile) matchingBrace(open int) int {
	if end, ok := file.braces[open]; ok {
		return end
	}
	return -1
}

// lineAt returns the one-based line containing offset.
func (file *sourceFile) lineAt(offset int) int {
	offset = min(offset, len(file.src))
	return sort.Search(len(file.lineStarts), func(index int) bool { return file.lineStarts[index] > offset })
}

// firstAtOrAfter returns the first sorted position at or after offset, or -1.
func firstAtOrAfter(positions []int, offset int) int {
	index := sort.SearchInts(positions, offset)
	if index == len(positions) {
		return -1
	}
	return positions[index]
}

// braceCursor reports brace depth at increasing offsets without rescanning
// the text before each query.
type braceCursor struct {
	text     string
	position int
	depth    int
}

func (cursor *braceCursor) at(offset int) int {
	if offset < cursor.position {
		cursor.position, cursor.depth = 0, 0
	}
	for ; cursor.position < offset; cursor.position++ {
		switch cursor.text[cursor.position] {
		case '{':
			cursor.depth++
		case '}':
			cursor.depth--
		}
	}
	return cursor.depth
}

func insideClass(offset int, classes []*classInfo) bool {
	for _, class := range classes {
		if offset > class.start && offset < class.end {
			return true
		}
	}
	return false
}
func exportedAt(source string, offset int) bool {
	prefix := source[max(0, offset-80):min(len(source), offset+16)]
	return strings.Contains(prefix, "export")
}
func defaultExportAt(source string, offset int) bool {
	segment := source[max(0, offset-16):min(len(source), offset+32)]
	return strings.Contains(segment, "export default")
}
func simpleType(value string) string {
	value = strings.TrimSpace(value)
	if isSimpleType(value) {
		return value
	}
	return ""
}
func isSimpleType(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		letter := character == '_' || character == '$' || (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z')
		if !letter && (index == 0 || character < '0' || character > '9') {
			return false
		}
	}
	return true
}
func precededByDot(source string, offset int) bool {
	for offset > 0 && (source[offset-1] == ' ' || source[offset-1] == '\t' || source[offset-1] == '\n') {
		offset--
	}
	return offset > 0 && source[offset-1] == '.'
}
func knownNonCall(name string) bool { return name == "require" || name == "super" }
func commentStart(source string, start int) int {
	prefix := source[:start]
	// Only a comment that ends right before the declaration can attach to it;
	// checking that first avoids searching the whole prefix for every symbol.
	if !strings.HasSuffix(strings.TrimRight(prefix, " \t\r\n\f\v"), "*/") {
		return start
	}
	if index := strings.LastIndex(prefix, "/**"); index >= 0 && strings.TrimSpace(prefix[index+2:]) != "" && strings.HasSuffix(strings.TrimSpace(prefix[index:]), "*/") {
		return index
	}
	return start
}
func jsDoc(source string, start int) string {
	segment := source[max(0, start-2048):start]
	begin, end := strings.LastIndex(segment, "/**"), strings.LastIndex(segment, "*/")
	if begin < 0 || end < begin+len("/**") {
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
