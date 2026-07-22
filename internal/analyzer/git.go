package analyzer

import (
	"bytes"
	"context"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type functionSpan struct {
	id     string
	key    string
	path   string
	start  int
	end    int
	source string
}

type diffHunk struct {
	newStart int
	lines    []string
}

type fileDiff struct {
	path   string
	header []string
	hunks  []diffHunk
}

var hunkHeaderPattern = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// captureGitSnapshot enriches functions with a non-fatal, scan-time Git view.
func captureGitSnapshot(ctx context.Context, root string, functions map[string]Function) GitSnapshot {
	repositoryOutput, err := gitOutput(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return GitSnapshot{ChangedFunctions: []ChangedFunction{}}
	}
	repositoryRoot := strings.TrimSpace(string(repositoryOutput))
	root = resolvedPath(root)
	repositoryRoot = resolvedPath(repositoryRoot)
	snapshot := GitSnapshot{Available: true, ChangedFunctions: []ChangedFunction{}}
	if branch, branchErr := gitOutput(ctx, repositoryRoot, "symbolic-ref", "--quiet", "--short", "HEAD"); branchErr == nil {
		snapshot.Branch = strings.TrimSpace(string(branch))
	}
	revision, revisionErr := gitOutput(ctx, repositoryRoot, "rev-parse", "--verify", "HEAD")
	if revisionErr == nil {
		snapshot.Revision = strings.TrimSpace(string(revision))
		snapshot.Detached = snapshot.Branch == ""
	}

	spans := currentFunctionSpans(repositoryRoot, functions)
	if len(spans) == 0 {
		return snapshot
	}
	rootRelative, err := filepath.Rel(repositoryRoot, root)
	if err != nil || strings.HasPrefix(rootRelative, ".."+string(filepath.Separator)) {
		return snapshot
	}
	pathspec := filepath.ToSlash(rootRelative)
	if pathspec == "" {
		pathspec = "."
	}

	if revisionErr != nil {
		paths, listErr := gitNullList(ctx, repositoryRoot, "ls-files", "--cached", "--others", "--exclude-standard", "--", pathspec)
		if listErr != nil {
			return snapshot
		}
		for _, path := range paths {
			markWholeFileNew(path, spans, functions)
		}
		return finishGitSnapshot(snapshot, functions)
	}

	baseline := baselineFunctionKeys(ctx, repositoryRoot, pathspec)
	patchOutput, patchErr := gitOutput(ctx, repositoryRoot, "diff", "--no-ext-diff", "--no-color", "--unified=3", "HEAD", "--", pathspec)
	if patchErr == nil {
		for _, file := range parseGitDiff(string(patchOutput)) {
			for _, span := range spans[file.path] {
				if diff := functionDiff(file, span); diff != "" {
					kind := "new"
					if baseline[span.key] {
						kind = "updated"
					}
					function := functions[span.id]
					function.Change = &FunctionChange{Kind: kind, Diff: diff}
					functions[span.id] = function
				}
			}
		}
	}
	untracked, untrackedErr := gitNullList(ctx, repositoryRoot, "ls-files", "--others", "--exclude-standard", "--", pathspec)
	if untrackedErr == nil {
		for _, path := range untracked {
			markWholeFileNew(path, spans, functions)
		}
	}
	return finishGitSnapshot(snapshot, functions)
}

func gitOutput(ctx context.Context, directory string, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", directory}, arguments...)...)
	return command.Output()
}

func gitNullList(ctx context.Context, directory string, arguments ...string) ([]string, error) {
	withNulls := append([]string(nil), arguments...)
	insertAt := len(withNulls)
	for index, argument := range withNulls {
		if argument == "--" {
			insertAt = index
			break
		}
	}
	withNulls = append(withNulls, "")
	copy(withNulls[insertAt+1:], withNulls[insertAt:])
	withNulls[insertAt] = "-z"
	output, err := gitOutput(ctx, directory, withNulls...)
	if err != nil {
		return nil, err
	}
	parts := bytes.Split(output, []byte{0})
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) > 0 {
			result = append(result, filepath.ToSlash(string(part)))
		}
	}
	return result, nil
}

func currentFunctionSpans(repositoryRoot string, functions map[string]Function) map[string][]functionSpan {
	byFileAndLine := make(map[string]map[int]Function)
	for _, function := range functions {
		if function.Anonymous {
			continue
		}
		path := filepath.Clean(function.File)
		if byFileAndLine[path] == nil {
			byFileAndLine[path] = make(map[int]Function)
		}
		byFileAndLine[path][function.Line] = function
	}
	result := make(map[string][]functionSpan)
	for filename, byLine := range byFileAndLine {
		contents, err := os.ReadFile(filename)
		if err != nil {
			continue
		}
		relative, err := filepath.Rel(repositoryRoot, resolvedPath(filename))
		if err != nil || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		path := filepath.ToSlash(relative)
		if !strings.HasSuffix(filename, ".go") {
			for _, function := range byLine {
				result[path] = append(result[path], functionSpan{
					id: function.ID, key: path + "|" + function.QualifiedName, path: path,
					start: function.Line, end: function.EndLine, source: function.Source,
				})
			}
			continue
		}
		fileSet := token.NewFileSet()
		parsed, err := parser.ParseFile(fileSet, filename, contents, parser.ParseComments)
		if err != nil {
			continue
		}
		for _, declaration := range parsed.Decls {
			functionDeclaration, ok := declaration.(*ast.FuncDecl)
			if !ok {
				continue
			}
			line := fileSet.Position(functionDeclaration.Pos()).Line
			function, ok := byLine[line]
			if !ok {
				continue
			}
			start := functionDeclaration.Pos()
			if functionDeclaration.Doc != nil {
				start = functionDeclaration.Doc.Pos()
			}
			startPosition := fileSet.Position(start)
			endPosition := fileSet.Position(functionDeclaration.End())
			source := ""
			if startPosition.Offset >= 0 && endPosition.Offset <= len(contents) && startPosition.Offset < endPosition.Offset {
				source = string(contents[startPosition.Offset:endPosition.Offset])
			}
			result[path] = append(result[path], functionSpan{
				id: function.ID, key: declarationKey(path, parsed.Name.Name, functionDeclaration), path: path,
				start: startPosition.Line, end: endPosition.Line, source: source,
			})
		}
	}
	return result
}

func resolvedPath(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

func baselineFunctionKeys(ctx context.Context, repositoryRoot string, pathspec string) map[string]bool {
	paths, err := gitNullList(ctx, repositoryRoot, "ls-tree", "-r", "--name-only", "HEAD", "--", pathspec)
	if err != nil {
		return map[string]bool{}
	}
	result := make(map[string]bool)
	for _, path := range paths {
		if !strings.HasSuffix(path, ".go") {
			if supportedJavaScriptPath(path) {
				contents, showErr := gitOutput(ctx, repositoryRoot, "show", "HEAD:"+path)
				if showErr == nil {
					for _, name := range javascriptDeclarationNames(string(contents)) {
						qualified := strings.TrimSuffix(path, filepath.Ext(path)) + "." + name
						result[path+"|"+qualified] = true
					}
				}
			}
			continue
		}
		contents, err := gitOutput(ctx, repositoryRoot, "show", "HEAD:"+path)
		if err != nil {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, contents, 0)
		if err != nil {
			continue
		}
		for _, declaration := range parsed.Decls {
			if functionDeclaration, ok := declaration.(*ast.FuncDecl); ok {
				result[declarationKey(path, parsed.Name.Name, functionDeclaration)] = true
			}
		}
	}
	return result
}

var javascriptDeclarationPattern = regexp.MustCompile(`(?m)(?:^|[;\n])\s*(?:export\s+)?(?:async\s+)?function\s+([A-Za-z_$][A-Za-z0-9_$]*)|(?:^|[;\n])\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z_$][A-Za-z0-9_$]*)\s*=>`)

func supportedJavaScriptPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".mjs", ".cjs", ".jsx", ".ts", ".mts", ".cts", ".tsx":
		return !strings.HasSuffix(path, ".d.ts")
	default:
		return false
	}
}

func javascriptDeclarationNames(source string) []string {
	matches := javascriptDeclarationPattern.FindAllStringSubmatch(source, -1)
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		if match[1] != "" {
			names = append(names, match[1])
			continue
		}
		if match[2] != "" {
			names = append(names, match[2])
		}
	}
	return names
}

func declarationKey(path string, packageName string, declaration *ast.FuncDecl) string {
	receiver := ""
	if declaration.Recv != nil && len(declaration.Recv.List) > 0 {
		var rendered bytes.Buffer
		if err := format.Node(&rendered, token.NewFileSet(), declaration.Recv.List[0].Type); err == nil {
			receiver = rendered.String()
		}
	}
	return strings.Join([]string{filepath.ToSlash(filepath.Dir(path)), packageName, receiver, declaration.Name.Name}, "|")
}

func parseGitDiff(patch string) []fileDiff {
	lines := strings.Split(strings.TrimSuffix(patch, "\n"), "\n")
	result := make([]fileDiff, 0)
	var current *fileDiff
	for index := 0; index < len(lines); index++ {
		line := lines[index]
		if strings.HasPrefix(line, "diff --git ") {
			result = append(result, fileDiff{})
			current = &result[len(result)-1]
			continue
		}
		if current == nil {
			continue
		}
		if strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
			current.header = append(current.header, line)
			if strings.HasPrefix(line, "+++ ") {
				current.path = gitPatchPath(strings.TrimPrefix(line, "+++ "))
			}
			continue
		}
		match := hunkHeaderPattern.FindStringSubmatch(line)
		if len(match) == 0 {
			continue
		}
		start, _ := strconv.Atoi(match[1])
		hunk := diffHunk{newStart: start, lines: []string{line}}
		for index+1 < len(lines) && !strings.HasPrefix(lines[index+1], "@@ ") && !strings.HasPrefix(lines[index+1], "diff --git ") {
			index++
			hunk.lines = append(hunk.lines, lines[index])
		}
		current.hunks = append(current.hunks, hunk)
	}
	return result
}

func gitPatchPath(value string) string {
	value = strings.TrimSuffix(value, "\t")
	if strings.HasPrefix(value, "\"") {
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		}
	}
	if value == "/dev/null" {
		return ""
	}
	return strings.TrimPrefix(value, "b/")
}

func functionDiff(file fileDiff, span functionSpan) string {
	selected := make([]diffHunk, 0)
	for _, hunk := range file.hunks {
		if hunkTouchesSpan(hunk, span.start, span.end) {
			selected = append(selected, hunk)
		}
	}
	if len(selected) == 0 {
		return ""
	}
	var builder strings.Builder
	for _, line := range file.header {
		builder.WriteString(line)
		builder.WriteByte('\n')
	}
	for _, hunk := range selected {
		builder.WriteString(strings.Join(hunk.lines, "\n"))
		builder.WriteByte('\n')
	}
	return builder.String()
}

func hunkTouchesSpan(hunk diffHunk, start int, end int) bool {
	newLine := hunk.newStart
	for _, line := range hunk.lines[1:] {
		if line == "" {
			continue
		}
		switch line[0] {
		case '+':
			if newLine >= start && newLine <= end {
				return true
			}
			newLine++
		case '-':
			if newLine >= start && newLine <= end {
				return true
			}
		case ' ':
			newLine++
		}
	}
	return false
}

func markWholeFileNew(path string, spans map[string][]functionSpan, functions map[string]Function) {
	for _, span := range spans[filepath.ToSlash(path)] {
		lineCount := 0
		if span.source != "" {
			lineCount = strings.Count(span.source, "\n") + 1
		}
		var builder strings.Builder
		builder.WriteString("--- /dev/null\n+++ b/")
		builder.WriteString(span.path)
		builder.WriteString("\n@@ -0,0 +")
		builder.WriteString(strconv.Itoa(span.start))
		builder.WriteString(",")
		builder.WriteString(strconv.Itoa(lineCount))
		builder.WriteString(" @@\n")
		for _, line := range strings.Split(span.source, "\n") {
			builder.WriteString("+")
			builder.WriteString(line)
			builder.WriteByte('\n')
		}
		function := functions[span.id]
		function.Change = &FunctionChange{Kind: "new", Diff: builder.String()}
		functions[span.id] = function
	}
}

func finishGitSnapshot(snapshot GitSnapshot, functions map[string]Function) GitSnapshot {
	for _, function := range functions {
		if function.Change == nil {
			continue
		}
		snapshot.ChangedFunctions = append(snapshot.ChangedFunctions, ChangedFunction{
			ID: function.ID, QualifiedName: function.QualifiedName, Package: function.Package,
			File: function.File, Line: function.Line, Test: function.Test, Kind: function.Change.Kind,
		})
	}
	sort.Slice(snapshot.ChangedFunctions, func(left int, right int) bool {
		if snapshot.ChangedFunctions[left].QualifiedName == snapshot.ChangedFunctions[right].QualifiedName {
			if snapshot.ChangedFunctions[left].File == snapshot.ChangedFunctions[right].File {
				return snapshot.ChangedFunctions[left].Line < snapshot.ChangedFunctions[right].Line
			}
			return snapshot.ChangedFunctions[left].File < snapshot.ChangedFunctions[right].File
		}
		return snapshot.ChangedFunctions[left].QualifiedName < snapshot.ChangedFunctions[right].QualifiedName
	})
	return snapshot
}
