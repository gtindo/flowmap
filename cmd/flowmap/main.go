// Command flowmap starts the local static code-reading workbench.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/gtindo/flowmap/internal/analyzer"
	"github.com/gtindo/flowmap/internal/server"
	"github.com/gtindo/flowmap/internal/telemetry"
)

const defaultAddress = "127.0.0.1:7878"

// version is replaced at release build time and remains dev for local builds.
var version = "dev"

// main parses the CLI and owns process-level error reporting.
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "flowmap:", err)
		os.Exit(1)
	}
}

// run executes the serve command and returns contextual failures to main.
func run(arguments []string) error {
	if len(arguments) == 1 && arguments[0] == "version" {
		fmt.Println(version)
		return nil
	}
	if len(arguments) == 0 || arguments[0] != "serve" {
		return fmt.Errorf("usage: flowmap <serve|version>; flowmap serve <module-path> [--addr %s] [--tags tag1,tag2] [--summarizer-command command] | flowmap serve --config projects.json", defaultAddress)
	}

	serveFlags := flag.NewFlagSet("serve", flag.ContinueOnError)
	address := serveFlags.String("addr", defaultAddress, "local listen address")
	buildTags := serveFlags.String("tags", "", "comma-separated Go build tags")
	configPath := serveFlags.String("config", "", "JSON project registry")
	summarizerCommand := serveFlags.String("summarizer-command", "", "opt-in JSON stdin/stdout summarizer command")

	serveArguments := arguments[1:]
	modulePath := ""
	if len(serveArguments) > 0 && !strings.HasPrefix(serveArguments[0], "-") {
		modulePath = serveArguments[0]
		serveArguments = serveArguments[1:]
	}

	if err := serveFlags.Parse(serveArguments); err != nil {
		return err
	}

	if modulePath == "" && serveFlags.NArg() == 1 {
		modulePath = serveFlags.Arg(0)
	}

	hasUnexpectedPositionals := modulePath != "" && len(arguments) > 1 && !strings.HasPrefix(arguments[1], "-") && serveFlags.NArg() > 0
	if serveFlags.NArg() > 1 || hasUnexpectedPositionals || (modulePath == "" && strings.TrimSpace(*configPath) == "") || (modulePath != "" && strings.TrimSpace(*configPath) != "") {
		return fmt.Errorf("serve requires exactly one module path or --config")
	}
	if strings.TrimSpace(*configPath) != "" && strings.TrimSpace(*buildTags) != "" {
		return fmt.Errorf("--tags cannot be used with --config; configure tags per project")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	shutdownTelemetry, telemetryEnabled, err := telemetry.Setup(ctx, version, os.Stdout)
	if err != nil {
		return err
	}
	defer func() {
		if err := shutdownTelemetry(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, "flowmap: telemetry shutdown:", err)
		}
	}()

	if telemetryEnabled {
		slog.InfoContext(ctx, "flowmap telemetry initialized", "version", version)
	}

	var summarizer server.Summarizer
	var cache *server.SummaryCache
	if strings.TrimSpace(*summarizerCommand) != "" {
		summarizer = server.CommandSummarizer{Command: *summarizerCommand}
		cache, err = server.NewSummaryCache()
		if err != nil {
			return err
		}
	}

	if strings.TrimSpace(*configPath) != "" {
		projects, err := loadProjects(*configPath)
		if err != nil {
			return err
		}

		app, err := server.NewProjects(projects, summarizer, cache)
		if err != nil {
			return err
		}

		fmt.Printf("Flowmap configured %d projects. Open http://%s\n", len(projects), *address)
		slog.InfoContext(ctx, "flowmap server starting", "address", *address, "projects", len(projects))
		return app.Listen(ctx, *address)
	}

	analysisConfigs, err := detectAnalysisConfigs(modulePath, splitTags(*buildTags), nil)
	if err != nil {
		return err
	}
	indexes := make(map[string]*analyzer.Index, len(analysisConfigs))
	functionCount := 0
	for _, analysisConfig := range analysisConfigs {
		index, analyzeErr := analyzer.Analyze(ctx, analysisConfig)
		if analyzeErr != nil {
			return analyzeErr
		}
		writeLoadWarning(os.Stderr, index.LoadReport)
		indexes[analysisConfig.Language] = index
		functionCount += len(index.Functions)
	}
	app, err := server.NewRescannableLanguages(indexes, summarizer, cache, analysisConfigs)
	if err != nil {
		return err
	}

	fmt.Printf("Flowmap indexed %d functions across %d language views. Open http://%s\n", functionCount, len(analysisConfigs), *address)
	slog.InfoContext(ctx, "flowmap server starting", "address", *address, "functions", functionCount, "languages", len(analysisConfigs))

	return app.Listen(ctx, *address)
}

type projectRegistry struct {
	Projects []projectEntry `json:"projects"`
}

type projectEntry struct {
	Name      string   `json:"name"`
	Path      string   `json:"path"`
	Tags      []string `json:"tags"`
	Languages []string `json:"languages"`
}

// loadProjects validates the JSON registry supplied to the multi-project server.
func loadProjects(path string) ([]server.ProjectConfig, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read project config: %w", err)
	}

	var registry projectRegistry
	if err := json.Unmarshal(contents, &registry); err != nil {
		return nil, fmt.Errorf("parse project config: %w", err)
	}

	if len(registry.Projects) == 0 {
		return nil, fmt.Errorf("project config must contain at least one project")
	}

	projects := make([]server.ProjectConfig, 0, len(registry.Projects))
	names := make(map[string]struct{}, len(registry.Projects))
	paths := make(map[string]struct{}, len(registry.Projects))
	for _, entry := range registry.Projects {
		name := strings.TrimSpace(entry.Name)
		root := strings.TrimSpace(entry.Path)
		if name == "" || root == "" {
			return nil, fmt.Errorf("project config entries require non-empty name and path")
		}
		if _, exists := names[name]; exists {
			return nil, fmt.Errorf("project config contains duplicate name %q", name)
		}
		absoluteRoot, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("resolve project %q path: %w", name, err)
		}
		if _, exists := paths[absoluteRoot]; exists {
			return nil, fmt.Errorf("project config contains duplicate path %q", root)
		}
		names[name] = struct{}{}
		paths[absoluteRoot] = struct{}{}
		analyses, configErr := detectAnalysisConfigs(absoluteRoot, normalizeTags(entry.Tags), entry.Languages)
		if configErr != nil {
			return nil, fmt.Errorf("configure project %q: %w", name, configErr)
		}
		projects = append(projects, server.ProjectConfig{Name: name, Analyses: analyses})
	}
	return projects, nil
}

func detectAnalysisConfigs(root string, tags []string, requested []string) ([]analyzer.Config, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve project root: %w", err)
	}
	detected := map[string]bool{}
	if _, statErr := os.Stat(filepath.Join(absRoot, "go.mod")); statErr == nil {
		detected[analyzer.LanguageGo] = true
	}
	jsFound := false
	_ = filepath.WalkDir(absRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || jsFound {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "vendor", "dist", "build", "coverage", ".next", "out":
				return filepath.SkipDir
			}
			return nil
		}
		extension := strings.ToLower(filepath.Ext(path))
		switch extension {
		case ".js", ".mjs", ".cjs", ".jsx", ".ts", ".mts", ".cts", ".tsx":
			if !strings.HasSuffix(path, ".d.ts") {
				jsFound = true
			}
		}
		return nil
	})
	if jsFound {
		detected[analyzer.LanguageJavaScript] = true
	}
	languages := requested
	if len(languages) == 0 {
		languages = make([]string, 0, len(detected))
		for _, language := range []string{analyzer.LanguageGo, analyzer.LanguageJavaScript} {
			if detected[language] {
				languages = append(languages, language)
			}
		}
	}
	configs := make([]analyzer.Config, 0, len(languages))
	seen := map[string]bool{}
	for _, language := range languages {
		language = strings.ToLower(strings.TrimSpace(language))
		if language != analyzer.LanguageGo && language != analyzer.LanguageJavaScript {
			return nil, fmt.Errorf("unsupported language %q", language)
		}
		if seen[language] {
			return nil, fmt.Errorf("duplicate language %q", language)
		}
		if !detected[language] {
			return nil, fmt.Errorf("no %s source detected beneath %s", language, absRoot)
		}
		seen[language] = true
		config := analyzer.Config{Root: absRoot, Language: language}
		if language == analyzer.LanguageGo {
			config.BuildTags = append([]string(nil), tags...)
		}
		configs = append(configs, config)
	}
	if len(configs) == 0 {
		return nil, fmt.Errorf("no supported source detected beneath %s", absRoot)
	}
	return configs, nil
}

func normalizeTags(tags []string) []string {
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		if tag = strings.TrimSpace(tag); tag != "" {
			result = append(result, tag)
		}
	}
	return result
}

// writeLoadWarning keeps partial package failures visible without stopping a usable scan.
func writeLoadWarning(writer io.Writer, report analyzer.LoadReport) {
	if report.HasFailures() {
		fmt.Fprintln(writer, "flowmap: warning:", report.String())
		if telemetry.Enabled() {
			slog.Warn("package load completed with failures", "diagnostics", len(report.Diagnostics))
		}
	}
}

// splitTags normalizes the optional build-tag list.
func splitTags(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))

	for _, part := range parts {
		if tag := strings.TrimSpace(part); tag != "" {
			result = append(result, tag)
		}
	}

	return result
}
