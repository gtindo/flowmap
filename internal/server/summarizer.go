package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gtindo/flowmap/internal/analyzer"
)

// SummaryRequest contains the minimum local context offered to an opt-in provider.
type SummaryRequest struct {
	QualifiedName string              `json:"qualified_name"`
	Signature     string              `json:"signature"`
	Source        string              `json:"source"`
	Documentation string              `json:"documentation,omitempty"`
	Contracts     []analyzer.Contract `json:"contracts,omitempty"`
}

// SummaryResult marks machine-generated intent explicitly.
type SummaryResult struct {
	Summary string `json:"summary"`
	Source  string `json:"source"`
	Cached  bool   `json:"cached"`
}

// Summarizer generates missing intent without coupling Flowmap to an AI vendor.
type Summarizer interface {
	Identity() string
	Summarize(ctx context.Context, request SummaryRequest) (string, error)
}

// CommandSummarizer exchanges one JSON request and response with an opt-in command.
type CommandSummarizer struct{ Command string }

// Identity separates cache entries produced by different configured commands.
func (summarizer CommandSummarizer) Identity() string { return "command:" + summarizer.Command }

// Summarize invokes the command and expects a JSON object containing summary.
func (summarizer CommandSummarizer) Summarize(ctx context.Context, request SummaryRequest) (string, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("encode summary request: %w", err)
	}
	command := exec.CommandContext(ctx, "sh", "-c", summarizer.Command)
	command.Stdin = bytes.NewReader(payload)
	var output, errorOutput bytes.Buffer
	command.Stdout, command.Stderr = &output, &errorOutput
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("run summarizer: %w: %s", err, strings.TrimSpace(errorOutput.String()))
	}
	var response struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		return "", fmt.Errorf("decode summarizer response: %w", err)
	}
	if response.Summary = strings.TrimSpace(response.Summary); response.Summary == "" {
		return "", fmt.Errorf("decode summarizer response: summary is empty")
	}
	return response.Summary, nil
}

// SummaryCache stores generated intent outside the analyzed repository.
type SummaryCache struct{ directory string }

// NewSummaryCache creates a private cache in the operating-system user cache.
func NewSummaryCache() (*SummaryCache, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("locate user cache: %w", err)
	}
	directory := filepath.Join(root, "flowmap", "summaries")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create summary cache: %w", err)
	}
	return &SummaryCache{directory: directory}, nil
}

// Get returns a cached summary keyed by provider and exact function context.
func (cache *SummaryCache) Get(identity string, request SummaryRequest) (string, bool) {
	contents, err := os.ReadFile(cache.path(identity, request))
	return string(contents), err == nil
}

// Put persists one generated summary in the private cache.
func (cache *SummaryCache) Put(identity string, request SummaryRequest, summary string) error {
	if err := os.WriteFile(cache.path(identity, request), []byte(summary), 0o600); err != nil {
		return fmt.Errorf("cache summary: %w", err)
	}
	return nil
}

// path derives a content-addressed cache filename.
func (cache *SummaryCache) path(identity string, request SummaryRequest) string {
	payload, _ := json.Marshal(request)
	digest := sha256.Sum256(append([]byte(identity+"\x00"), payload...))
	return filepath.Join(cache.directory, hex.EncodeToString(digest[:])+".txt")
}
