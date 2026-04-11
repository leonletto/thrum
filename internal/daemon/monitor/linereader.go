package monitor

import (
	"bufio"
	"io"
)

// Line is a single line read from a child process stdout, possibly truncated
// if it exceeded the reader's max length.
type Line struct {
	Content   string
	Truncated bool
}

// LineReader reads lines from an io.Reader with a per-line byte cap.
// If a line exceeds cap, the first cap bytes are returned as a Line with
// Truncated=true, and the remaining bytes up to the next newline are
// silently discarded. This prevents a single oversized line from OOM'ing
// the daemon or flooding an agent's context window.
type LineReader struct {
	br     *bufio.Reader
	maxLen int
}

// NewLineReader wraps the given reader with a maxLen byte cap per line.
func NewLineReader(r io.Reader, maxLen int) *LineReader {
	return &LineReader{
		br:     bufio.NewReaderSize(r, maxLen*2),
		maxLen: maxLen,
	}
}

// ReadLine returns the next line. Returns io.EOF when the underlying reader
// is exhausted (with no final partial line) or when an incomplete final
// line has been returned.
func (lr *LineReader) ReadLine() (Line, error) {
	var buf []byte
	truncated := false
	for {
		chunk, err := lr.br.ReadSlice('\n')
		if len(chunk) > 0 {
			if !truncated {
				if len(buf)+len(chunk) > lr.maxLen {
					take := lr.maxLen - len(buf)
					buf = append(buf, chunk[:take]...)
					truncated = true
				} else {
					buf = append(buf, chunk...)
				}
			}
		}
		if err == bufio.ErrBufferFull {
			// Didn't hit newline — keep draining until we do.
			truncated = true
			continue
		}
		if err == io.EOF {
			if len(buf) == 0 {
				return Line{}, io.EOF
			}
			// Strip trailing newline if present.
			return finalLine(buf, truncated), nil
		}
		if err != nil {
			return Line{}, err
		}
		// Got a newline — line is complete.
		return finalLine(buf, truncated), nil
	}
}

func finalLine(buf []byte, truncated bool) Line {
	if n := len(buf); n > 0 && buf[n-1] == '\n' {
		buf = buf[:n-1]
	}
	if n := len(buf); n > 0 && buf[n-1] == '\r' {
		buf = buf[:n-1]
	}
	return Line{Content: string(buf), Truncated: truncated}
}
