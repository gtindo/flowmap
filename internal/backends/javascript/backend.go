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
	functionPattern       = regexp.MustCompile(`\b(?:export\s+(?:default\s+)?)?(?:async\s+)?function\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*\(([^)]*)\)\s*(?::\s*([^={]+))?\s*\{`)
	arrowPattern          = regexp.MustCompile(`\b(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*(?::[^=;]+)?=\s*(?:async\s*)?(\([^)]*\)|[A-Za-z_$][A-Za-z0-9_$]*)\s*=>`)
	classPattern          = regexp.MustCompile(`\b(?:export\s+(?:default\s+)?)?class\s+([A-Za-z_$][A-Za-z0-9_$]*)(?:\s+extends\s+([A-Za-z_$][A-Za-z0-9_$]*))?[^\{]*\{`)
	classExpression       = regexp.MustCompile(`\b(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*(?::[^=;]+)?=\s*class(?:\s+[A-Za-z_$][A-Za-z0-9_$]*)?(?:\s+extends\s+([A-Za-z_$][A-Za-z0-9_$]*))?[^\{]*\{`)
	methodPattern         = regexp.MustCompile(`(?m)(?:^|[;{}])\s*(?:(?:public|private|protected|readonly|abstract|declare|override)\s+)*(?:(static)\s+)?(?:(async)\s+)?(?:(get|set)\s+)?(\*?)\s*(constructor|[A-Za-z_$][A-Za-z0-9_$]*)\s*\(([^)]*)\)\s*(?::\s*[^\{=]+)?\s*\{`)
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

var supportedExtensions = map[string]bool{
	".js": true, ".mjs": true, ".cjs": true, ".jsx": true,
	".ts": true, ".mts": true, ".cts": true, ".tsx": true,
}

var ignoredDirectories = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true, "build": true,
	"coverage": true, ".next": true, "out": true,
}

type sourceFile struct {
	abs string
	rel string
	src string

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

	class      *classInfo
	memberName string
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

	linkModules(files, byFile)
	relationships := collectRelationships(records)
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
		files = append(files, &sourceFile{abs: path, rel: filepath.ToSlash(relative), src: string(contents), functions: map[string]*symbolRecord{}, classes: map[string]*classInfo{}, exportFunctions: map[string]*symbolRecord{}, exportClasses: map[string]*classInfo{}, dependencies: map[string]*symbolRecord{}, classAliases: map[string]*classInfo{}, moduleAliases: map[string]*sourceFile{}})
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

// DeclarationNames returns callable names for JavaScript-family source. It is
// used by Git attribution so class methods use the same owner-qualified keys as
// the semantic snapshot.
// Operations (Pure): extracts callable names from explicit source text.
func DeclarationNames(source string) []string {
	file := &sourceFile{
		rel:             "source.ts",
		src:             source,
		functions:       map[string]*symbolRecord{},
		classes:         map[string]*classInfo{},
		exportFunctions: map[string]*symbolRecord{},
		exportClasses:   map[string]*classInfo{},
		dependencies:    map[string]*symbolRecord{},
		classAliases:    map[string]*classInfo{},
		moduleAliases:   map[string]*sourceFile{},
	}
	records, _ := parse(file)
	names := make([]string, 0, len(records))
	for _, record := range records {
		names = append(names, record.symbol.Name)
	}
	return names
}

func parse(file *sourceFile) ([]*symbolRecord, error) {
	options := typescriptestree.NewBuilder().WithSourceType(typescriptestree.SourceTypeModule).WithFilePath(file.abs).WithLoc(true).WithRange(true).MustBuild()
	// Validation is intentionally best effort: the standalone extractor still handles
	// syntax that this parser version does not yet accept.
	_, _ = typescriptestree.Parse(importPattern.ReplaceAllString(file.src, ""), options)

	masked := maskSource(file.src)
	classes := extractClasses(file, masked)
	records := make([]*symbolRecord, 0)
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
		end := matchingBrace(masked, brace)
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
		if braceDepth(masked[bodyStart:brace]) != 0 {
			continue
		}
		memberName := name
		if name == "constructor" {
			memberName = "constructor"
		}
		record := addRecord(file, class, class.name+"."+memberName, memberName, parameters, start, brace, "", match[2] >= 0, true)
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

func extractClassFields(class *classInfo, body string) {
	for _, match := range fieldTypePattern.FindAllStringSubmatchIndex(body, -1) {
		if braceDepth(body[:match[0]]) != 0 {
			continue
		}
		class.fields[body[match[2]:match[3]]] = body[match[4]:match[5]]
	}
}

func addRecord(file *sourceFile, class *classInfo, name, memberName, parameters string, declarationStart, bodyStart int, returnType string, static, exported bool) *symbolRecord {
	end := expressionEnd(file.src, bodyStart)
	start := commentStart(file.src, declarationStart)
	line, endLine := lineAt(file.src, start), lineAt(file.src, end)
	qualified := strings.TrimSuffix(file.rel, filepath.Ext(file.rel)) + "." + name
	identity := file.rel + "|" + qualified + fmt.Sprintf("|%d", declarationStart)
	digest := sha256.Sum256([]byte(identity))
	identifier := hex.EncodeToString(digest[:16])
	kind := semantic.SymbolFunction
	if class != nil {
		kind = semantic.SymbolMethod
	}
	record := &symbolRecord{symbol: semantic.Symbol{ID: identifier, ChangeKey: file.rel + "|" + qualified, Language: "javascript", Kind: kind, Name: name, QualifiedName: qualified, Package: filepath.ToSlash(filepath.Dir(file.rel)), Location: semantic.Location{File: file.abs, Line: line, EndLine: endLine}, Source: file.src[start:end], Documentation: jsDoc(file.src, declarationStart), Signature: semantic.Signature{Display: name + "(" + strings.TrimSpace(parameters) + ")", Parameters: parameterList(parameters)}, Test: isTestFile(file.rel)}, file: file, body: file.src[bodyStart:end], class: class, memberName: memberName, static: static, returnType: simpleType(returnType), exported: exported}
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

func linkImports(file *sourceFile, files map[string]*sourceFile) {
	for _, match := range importPattern.FindAllStringSubmatch(file.src, -1) {
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
	for _, match := range requirePattern.FindAllStringSubmatch(file.src, -1) {
		if strings.HasPrefix(match[2], ".") {
			if target := resolveModule(file.rel, match[2], files); target != nil {
				file.moduleAliases[match[1]] = target
			}
		}
	}
	for _, match := range exportNamedPattern.FindAllStringSubmatch(file.src, -1) {
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
	for _, match := range exportStarPattern.FindAllStringSubmatch(file.src, -1) {
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

func collectRelationships(records []*symbolRecord) []semantic.Relationship {
	edges := make([]semantic.Relationship, 0)
	seen := map[string]bool{}
	add := func(from, to *symbolRecord, kind string, dynamic bool) {
		if from == nil || to == nil || from == to {
			return
		}
		key := from.symbol.ID + "|" + to.symbol.ID + "|" + kind
		if seen[key] {
			return
		}
		seen[key] = true
		edges = append(edges, semantic.Relationship{FromID: from.symbol.ID, ToID: to.symbol.ID, Kind: kind, Dynamic: dynamic})
	}
	for _, record := range records {
		clean := maskSource(record.body)
		bindings := receiverBindings(record)
		for _, match := range memberCallPattern.FindAllStringSubmatch(clean, -1) {
			receiver, method := match[1], match[2]
			if target, direct := resolveMember(record, receiver, method, bindings); target != nil {
				kind := semantic.RelationshipCall
				if !direct {
					kind = semantic.RelationshipDependency
				}
				add(record, target, kind, !direct)
				continue
			}
		}
		for _, match := range directCallPattern.FindAllStringSubmatchIndex(clean, -1) {
			name := clean[match[2]:match[3]]
			if keyword(name) || name == record.memberName || precededByDot(clean, match[0]) {
				continue
			}
			if target := record.file.functions[name]; target != nil {
				add(record, target, semantic.RelationshipCall, false)
				continue
			}
			if target := record.file.dependencies[name]; target != nil {
				add(record, target, semantic.RelationshipCall, false)
				continue
			}
			if !knownNonCall(name) {
				record.symbol.Facts = append(record.symbol.Facts, semantic.Fact{Kind: semantic.FactExternalCall, Name: name})
			}
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
	clean := maskSource(record.body)
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
		if end := matchingBrace(maskSource(source), start+open); end >= 0 {
			return end + 1
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
func matchingBrace(source string, open int) int {
	if open < 0 || open >= len(source) || source[open] != '{' {
		return -1
	}
	depth := 0
	for index := open; index < len(source); index++ {
		switch source[index] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return index
			}
		}
	}
	return -1
}
func braceDepth(source string) int {
	depth := 0
	for index := range source {
		if source[index] == '{' {
			depth++
		}
		if source[index] == '}' {
			depth--
		}
	}
	return depth
}
func maskSource(source string) string {
	output := []byte(source)
	for index := 0; index < len(output); {
		if output[index] == '/' && index+1 < len(output) && output[index+1] == '/' {
			for index < len(output) && output[index] != '\n' {
				output[index] = ' '
				index++
			}
			continue
		}
		if output[index] == '/' && index+1 < len(output) && output[index+1] == '*' {
			output[index], output[index+1] = ' ', ' '
			index += 2
			for index+1 < len(output) && !(output[index] == '*' && output[index+1] == '/') {
				if output[index] != '\n' {
					output[index] = ' '
				}
				index++
			}
			if index+1 < len(output) {
				output[index], output[index+1] = ' ', ' '
				index += 2
			}
			continue
		}
		if output[index] == '\'' || output[index] == '"' || output[index] == '`' {
			quote := output[index]
			output[index] = ' '
			index++
			for index < len(output) {
				if output[index] == '\\' {
					output[index] = ' '
					index += 2
					continue
				}
				if output[index] == quote {
					output[index] = ' '
					index++
					break
				}
				if output[index] != '\n' {
					output[index] = ' '
				}
				index++
			}
			continue
		}
		index++
	}
	return string(output)
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
	return regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`).MatchString(value)
}
func precededByDot(source string, offset int) bool {
	for offset > 0 && (source[offset-1] == ' ' || source[offset-1] == '\t' || source[offset-1] == '\n') {
		offset--
	}
	return offset > 0 && source[offset-1] == '.'
}
func knownNonCall(name string) bool { return name == "require" || name == "super" }
func lineAt(source string, offset int) int {
	return strings.Count(source[:min(offset, len(source))], "\n") + 1
}
func commentStart(source string, start int) int {
	prefix := source[:start]
	if index := strings.LastIndex(prefix, "/**"); index >= 0 && strings.TrimSpace(prefix[index+2:]) != "" && strings.HasSuffix(strings.TrimSpace(prefix[index:]), "*/") {
		return index
	}
	return start
}
func jsDoc(source string, start int) string {
	segment := source[max(0, start-2048):start]
	begin, end := strings.LastIndex(segment, "/**"), strings.LastIndex(segment, "*/")
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
	_, exists := map[string]bool{"if": true, "for": true, "while": true, "switch": true, "catch": true, "function": true, "return": true, "new": true, "typeof": true, "import": true, "export": true, "class": true}[value]
	return exists
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
