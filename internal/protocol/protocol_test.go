package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestFrameRoundTripAndHeaderRules(t *testing.T) {
	var buffer bytes.Buffer
	writer := NewFrameWriter(&buffer)
	if err := writer.Write([]byte(`{"jsonrpc":"2.0","method":"exit"}`)); err != nil {
		t.Fatal(err)
	}
	if got := buffer.String(); got != "Content-Length: 33\r\n\r\n{\"jsonrpc\":\"2.0\",\"method\":\"exit\"}" {
		t.Fatalf("frame = %q", got)
	}

	stream := "content-LENGTH: 2\r\nContent-Type: ignored\r\n\r\n{}" + buffer.String()
	reader := NewFrameReader(strings.NewReader(stream))
	first, err := reader.Read()
	if err != nil || string(first) != "{}" {
		t.Fatalf("first frame = %q, %v", first, err)
	}
	second, err := reader.Read()
	if err != nil || !json.Valid(second) {
		t.Fatalf("second frame = %q, %v", second, err)
	}
	if _, err := reader.Read(); !errors.Is(err, io.EOF) {
		t.Fatalf("clean end error = %v", err)
	}
}

func TestFrameReaderRejectsUnrecoverableHeaders(t *testing.T) {
	cases := map[string]string{
		"missing":   "Content-Type: x\r\n\r\n{}",
		"duplicate": "Content-Length: 2\r\nContent-Length: 2\r\n\r\n{}",
		"malformed": "Content-Length: two\r\n\r\n{}",
		"negative":  "Content-Length: -1\r\n\r\n{}",
		"too large": "Content-Length: 999999999999\r\n\r\n{}",
		"no colon":  "garbage\r\n\r\n{}",
		"truncated": "Content-Length: 10\r\n\r\n{}",
	}
	for name, stream := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NewFrameReader(strings.NewReader(stream)).Read()
			if !errors.Is(err, ErrFraming) {
				t.Fatalf("Read() error = %v, want ErrFraming", err)
			}
		})
	}
}

func TestFileURIRoundTrip(t *testing.T) {
	for _, path := range []string{"/Users/alex/src/acme", "/tmp/with space/a#b.go"} {
		uri := FileURI(path)
		if !strings.HasPrefix(uri, "file:///") {
			t.Fatalf("FileURI(%q) = %q", path, uri)
		}
		back, err := PathFromURI(uri)
		if err != nil || back != path {
			t.Fatalf("PathFromURI(%q) = %q, %v", uri, back, err)
		}
	}

	for _, invalid := range []string{"https://example.com/x", "file:relative/path", "/plain/path", "file://remote/share"} {
		if _, err := PathFromURI(invalid); err == nil {
			t.Fatalf("PathFromURI(%q) accepted an invalid URI", invalid)
		}
	}
}

func TestValidID(t *testing.T) {
	cases := map[string]bool{`1`: true, `"abc"`: true, `-7`: true, `1.5`: false, `null`: false, `true`: false, `{}`: false, ``: false}
	for raw, want := range cases {
		if got := ValidID(json.RawMessage(raw)); got != want {
			t.Fatalf("ValidID(%s) = %t, want %t", raw, got, want)
		}
	}
}
