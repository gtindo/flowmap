package protocol

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// MaxFrameBytes bounds one payload so a corrupt header cannot exhaust memory.
const MaxFrameBytes = 64 << 20

const contentLengthHeader = "content-length"

// ErrFraming marks an unrecoverable stream framing failure.
var ErrFraming = errors.New("protocol framing error")

// FrameReader reads Content-Length framed payloads.
type FrameReader struct {
	reader *bufio.Reader
}

// NewFrameReader wraps a byte stream for framed reads.
func NewFrameReader(reader io.Reader) *FrameReader {
	return &FrameReader{reader: bufio.NewReader(reader)}
}

// Read returns the next payload. It returns io.EOF only at a clean frame
// boundary and wraps ErrFraming when the stream cannot be resynchronized.
// Side Effect (Edge): consumes bytes from the underlying stream.
func (frameReader *FrameReader) Read() ([]byte, error) {
	length, err := frameReader.readHeaders()
	if err != nil {
		return nil, err
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(frameReader.reader, payload); err != nil {
		return nil, fmt.Errorf("%w: read %d byte payload: %w", ErrFraming, length, err)
	}

	return payload, nil
}

func (frameReader *FrameReader) readHeaders() (int, error) {
	length := -1
	sawHeader := false

	for {
		line, err := frameReader.reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) && line == "" && !sawHeader {
				return 0, io.EOF
			}
			return 0, fmt.Errorf("%w: read header: %w", ErrFraming, err)
		}

		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line == "" {
			break
		}
		sawHeader = true

		name, value, found := strings.Cut(line, ":")
		if !found {
			return 0, fmt.Errorf("%w: malformed header %q", ErrFraming, line)
		}
		if !strings.EqualFold(strings.TrimSpace(name), contentLengthHeader) {
			continue
		}
		if length >= 0 {
			return 0, fmt.Errorf("%w: duplicate Content-Length header", ErrFraming)
		}

		parsed, parseErr := strconv.Atoi(strings.TrimSpace(value))
		if parseErr != nil || parsed < 0 || parsed > MaxFrameBytes {
			return 0, fmt.Errorf("%w: invalid Content-Length %q", ErrFraming, strings.TrimSpace(value))
		}
		length = parsed
	}

	if length < 0 {
		return 0, fmt.Errorf("%w: missing Content-Length header", ErrFraming)
	}
	return length, nil
}

// FrameWriter writes Content-Length framed payloads. It is safe for
// concurrent use so responses and notifications never interleave.
type FrameWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

// NewFrameWriter wraps a byte stream for framed writes.
func NewFrameWriter(writer io.Writer) *FrameWriter {
	return &FrameWriter{writer: writer}
}

// Write emits one complete frame.
// Side Effect (Edge): writes to the underlying stream.
func (frameWriter *FrameWriter) Write(payload []byte) error {
	frameWriter.mu.Lock()
	defer frameWriter.mu.Unlock()

	header := "Content-Length: " + strconv.Itoa(len(payload)) + "\r\n\r\n"
	frame := make([]byte, 0, len(header)+len(payload))
	frame = append(append(frame, header...), payload...)

	if _, err := frameWriter.writer.Write(frame); err != nil {
		return fmt.Errorf("write frame: %w", err)
	}
	return nil
}
