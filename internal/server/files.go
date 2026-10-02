package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gtindo/flowmap/internal/protocol"
)

// maxExplorerFiles bounds the listed files so a huge checkout cannot stall
// the browser; files that contain analyzed functions are always kept.
const maxExplorerFiles = 20000

// explorerSkippedDirectories are dependency and VCS directories omitted when
// the project is not a Git repository and .gitignore cannot be consulted.
var explorerSkippedDirectories = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
}

// ExplorerResponse is the browser file explorer's view of one language view.
type ExplorerResponse struct {
	Files     []ExplorerFile `json:"files"`
	Truncated bool           `json:"truncated"`
}

// ExplorerFile is one project file and the analyzed functions it declares.
type ExplorerFile struct {
	Path      string             `json:"path"`
	Functions []ExplorerFunction `json:"functions"`
}

// ExplorerFunction is the compact function entry listed under its file.
type ExplorerFunction struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	QualifiedName  string `json:"qualified_name"`
	Package        string `json:"package"`
	Line           int    `json:"line"`
	EndLine        int    `json:"end_line"`
	Classification string `json:"classification"`
	Public         bool   `json:"public"`
	Test           bool   `json:"test"`
}

// listProjectFiles lists root-relative, slash-separated file paths. Inside a
// Git repository it honors .gitignore; otherwise it walks the directory and
// skips dependency and hidden directories. The flag reports truncation.
// Side Effect (Edge): runs Git or reads the filesystem.
func listProjectFiles(ctx context.Context, root string) ([]string, bool, error) {
	paths, err := gitProjectFiles(ctx, root)
	if err != nil {
		paths, err = walkProjectFiles(ctx, root)
	}
	if err != nil {
		return nil, false, err
	}

	sort.Strings(paths)
	if len(paths) > maxExplorerFiles {
		return paths[:maxExplorerFiles], true, nil
	}
	return paths, false, nil
}

// gitProjectFiles lists tracked and untracked, non-ignored files that still
// exist in the working tree.
func gitProjectFiles(ctx context.Context, root string) ([]string, error) {
	listed, err := gitNullList(ctx, root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	deleted, err := gitNullList(ctx, root, "ls-files", "-z", "--deleted")
	if err != nil {
		return nil, err
	}

	missing := make(map[string]bool, len(deleted))
	for _, path := range deleted {
		missing[path] = true
	}

	seen := make(map[string]bool, len(listed))
	result := make([]string, 0, len(listed))
	for _, path := range listed {
		if missing[path] || seen[path] {
			continue
		}
		seen[path] = true
		result = append(result, path)
	}
	return result, nil
}

func gitNullList(ctx context.Context, directory string, arguments ...string) ([]string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", directory}, arguments...)...)
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", arguments[0], err)
	}

	parts := bytes.Split(output, []byte{0})
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) > 0 {
			result = append(result, string(part))
		}
	}
	return result, nil
}

// walkProjectFiles is the non-Git fallback. It stops early once the listing
// is known to be truncated.
func walkProjectFiles(ctx context.Context, root string) ([]string, error) {
	result := make([]string, 0)
	errEnough := errors.New("enough files")

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		if entry.IsDir() {
			name := entry.Name()
			if path != root && (explorerSkippedDirectories[name] || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}

		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		result = append(result, filepath.ToSlash(relative))
		if len(result) > maxExplorerFiles {
			return errEnough
		}
		return nil
	})
	if err != nil && !errors.Is(err, errEnough) {
		return nil, fmt.Errorf("list project files: %w", err)
	}
	return result, nil
}

// fileListing groups symbols under the root-relative files that declare them
// and merges in the listed paths, so ignored-but-analyzed files still appear.
// Symbols located outside every root are omitted because they have no place
// in the project tree. Any of roots may match, which tolerates symlinked roots.
// Operations (Pure): data grouping and ordering only.
func fileListing(roots []string, paths []string, symbols []protocol.SymbolSummary) []ExplorerFile {
	byPath := make(map[string][]ExplorerFunction, len(paths))
	for _, path := range paths {
		byPath[path] = nil
	}

	for _, symbol := range symbols {
		path, ok := projectRelativePath(roots, uriPath(symbol.Location.URI))
		if !ok {
			continue
		}

		byPath[path] = append(byPath[path], ExplorerFunction{
			ID:             symbol.SymbolID,
			Name:           symbol.Name,
			QualifiedName:  symbol.QualifiedName,
			Package:        symbol.Namespace,
			Line:           symbol.Location.StartLine,
			EndLine:        symbol.Location.EndLine,
			Classification: symbol.Classification,
			Public:         symbol.Public,
			Test:           symbol.Test,
		})
	}

	files := make([]ExplorerFile, 0, len(byPath))
	for path, functions := range byPath {
		if functions == nil {
			functions = []ExplorerFunction{}
		}
		sort.Slice(functions, func(left, right int) bool {
			if functions[left].Line != functions[right].Line {
				return functions[left].Line < functions[right].Line
			}
			return functions[left].QualifiedName < functions[right].QualifiedName
		})
		files = append(files, ExplorerFile{Path: path, Functions: functions})
	}

	sort.Slice(files, func(left, right int) bool { return files[left].Path < files[right].Path })
	return files
}

// projectRelativePath returns path relative to the first root containing it.
func projectRelativePath(roots []string, path string) (string, bool) {
	for _, root := range roots {
		if root == "" {
			continue
		}

		relative, err := filepath.Rel(root, path)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			continue
		}
		return filepath.ToSlash(relative), true
	}
	return "", false
}
