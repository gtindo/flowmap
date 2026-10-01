package protocol

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

const fileScheme = "file"

// FileURI converts an absolute filesystem path into a file: URI.
// Operations (Pure): string transformation only.
func FileURI(path string) string {
	slashed := filepath.ToSlash(path)
	if !strings.HasPrefix(slashed, "/") {
		// Windows drive paths need a leading slash in the URI path component.
		slashed = "/" + slashed
	}

	return (&url.URL{Scheme: fileScheme, Path: slashed}).String()
}

// PathFromURI converts an absolute file: URI into a filesystem path.
// Operations (Pure): string transformation only.
func PathFromURI(uri string) (string, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("parse URI %q: %w", uri, err)
	}

	if parsed.Scheme != fileScheme {
		return "", fmt.Errorf("URI %q is not a file: URI", uri)
	}
	if parsed.Host != "" && parsed.Host != "localhost" {
		return "", fmt.Errorf("URI %q names a remote host", uri)
	}
	if !strings.HasPrefix(parsed.Path, "/") {
		return "", fmt.Errorf("URI %q is not absolute", uri)
	}

	path := parsed.Path
	if len(path) >= 3 && path[2] == ':' {
		// Strip the URI slash before a Windows drive letter.
		path = path[1:]
	}

	return filepath.Clean(filepath.FromSlash(path)), nil
}
