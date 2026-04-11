package monitor

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLineReader_ShortLines(t *testing.T) {
	input := "foo\nbar\nbaz\n"
	r := NewLineReader(strings.NewReader(input), 2048)
	got, err := collectLines(r)
	require.NoError(t, err)
	assert.Equal(t, []Line{
		{Content: "foo", Truncated: false},
		{Content: "bar", Truncated: false},
		{Content: "baz", Truncated: false},
	}, got)
}

func TestLineReader_TruncatesOversizeLine(t *testing.T) {
	long := strings.Repeat("A", 3000) + "\n"
	r := NewLineReader(strings.NewReader(long), 2048)
	lines, err := collectLines(r)
	require.NoError(t, err)
	require.Len(t, lines, 1)
	assert.Equal(t, 2048, len(lines[0].Content))
	assert.True(t, lines[0].Truncated)
	assert.Equal(t, strings.Repeat("A", 2048), lines[0].Content)
}

func TestLineReader_ResyncsAtNextNewline(t *testing.T) {
	input := strings.Repeat("A", 3000) + "\nnormal line\n"
	r := NewLineReader(strings.NewReader(input), 2048)
	lines, err := collectLines(r)
	require.NoError(t, err)
	require.Len(t, lines, 2)
	assert.True(t, lines[0].Truncated)
	assert.Equal(t, "normal line", lines[1].Content)
	assert.False(t, lines[1].Truncated)
}

func TestLineReader_NoTrailingNewline(t *testing.T) {
	input := "incomplete"
	r := NewLineReader(strings.NewReader(input), 2048)
	lines, err := collectLines(r)
	require.NoError(t, err)
	require.Len(t, lines, 1)
	assert.Equal(t, "incomplete", lines[0].Content)
}

func TestLineReader_EmptyInput(t *testing.T) {
	r := NewLineReader(bytes.NewReader(nil), 2048)
	lines, err := collectLines(r)
	require.NoError(t, err)
	assert.Empty(t, lines)
}

// collectLines drains the reader into a slice for assertions.
func collectLines(r *LineReader) ([]Line, error) {
	var out []Line
	for {
		line, err := r.ReadLine()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		out = append(out, line)
	}
}
