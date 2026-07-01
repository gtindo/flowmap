// Command flowmap starts the local static code-reading workbench.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/gtindo/flowmap/internal/analyzer"
	"github.com/gtindo/flowmap/internal/server"
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
		return fmt.Errorf("usage: flowmap <serve|version>; flowmap serve <module-path> [--addr %s] [--tags tag1,tag2] [--summarizer-command command]", defaultAddress)
	}
	serveFlags := flag.NewFlagSet("serve", flag.ContinueOnError)
	address := serveFlags.String("addr", defaultAddress, "local listen address")
	buildTags := serveFlags.String("tags", "", "comma-separated Go build tags")
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
	if modulePath == "" || serveFlags.NArg() > 1 || hasUnexpectedPositionals {
		return fmt.Errorf("serve requires exactly one module path")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	index, err := analyzer.Analyze(ctx, analyzer.Config{Root: modulePath, BuildTags: splitTags(*buildTags)})
	if err != nil {
		return err
	}
	writeLoadWarning(os.Stderr, index.LoadReport)
	var summarizer server.Summarizer
	var cache *server.SummaryCache
	if strings.TrimSpace(*summarizerCommand) != "" {
		summarizer = server.CommandSummarizer{Command: *summarizerCommand}
		cache, err = server.NewSummaryCache()
		if err != nil {
			return err
		}
	}
	app, err := server.New(index, summarizer, cache)
	if err != nil {
		return err
	}
	fmt.Printf("Flowmap indexed %d functions. Open http://%s\n", len(index.Functions), *address)
	return app.Listen(ctx, *address)
}

func writeLoadWarning(writer io.Writer, report analyzer.LoadReport) {
	if report.HasFailures() {
		fmt.Fprintln(writer, "flowmap: warning:", report.String())
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
