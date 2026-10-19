package codeparse

import (
	"bytes"
	"sort"
)

// Shapes of the chunks of a file indexed as text (plan 0012: "other text
// files by 60-line windows with 10 lines of overlap"), and the cap on the
// chunks of one file.
const (
	TextWindowLines   = 60
	TextWindowOverlap = 10
	// MaxChunks caps the chunks of one file; the rest of a huge file stays
	// searchable by its symbols and full text only.
	MaxChunks = 1000
)

// binaryScan is how much of a file is searched for a NUL byte, the sign of
// a binary file that gets no chunks.
const binaryScan = 8 << 10

// TextFacts are the facts of a file indexed as text: line-window chunks and
// no symbols, imports or references. cause is the reason a file of a
// language fell back to text (ParseTimeout, ParseFailed, ParseNoGrammar) and
// lang its language; both are "" for a file with no language at all.
func TextFacts(lang string, content []byte, cause string) Facts {
	if cause == "" {
		lang = ""
	}
	return Facts{Lang: lang, ParseError: cause, Chunks: TextWindows(content)}
}

// TextWindows cuts content into windows of TextWindowLines lines, each
// overlapping the previous one by TextWindowOverlap lines, at most
// MaxChunks of them. A binary file (a NUL byte near its start) and an empty
// one have none.
func TextWindows(content []byte) []Chunk {
	if len(content) == 0 || isBinary(content) {
		return nil
	}
	lines := newLineIndex(content)
	n := lines.count()
	var chunks []Chunk
	for first := 0; first < n && len(chunks) < MaxChunks; first += TextWindowLines - TextWindowOverlap {
		last := min(first+TextWindowLines, n) - 1
		start := lines.start(first)
		end := lines.end(last)
		chunks = append(chunks, newChunk(lines, ChunkWindow, "", first+1, 1, start, end, string(content[start:lines.next(last)])))
		if last == n-1 {
			break
		}
	}
	return chunks
}

func isBinary(content []byte) bool {
	return bytes.IndexByte(content[:min(len(content), binaryScan)], 0) >= 0
}

// newChunk builds a chunk covering content[start:end] whose text is text.
// Its ID is ChunkID(symbolID, idLine, part): idLine is the 1-based first
// line of the section or window it is a part of, part counts from 1.
func newChunk(lines *lineIndex, kind, symbolID string, idLine, part, start, end int, text string) Chunk {
	s := lines.pos(start)
	return Chunk{
		ID:       ChunkID(symbolID, idLine, part),
		Kind:     kind,
		SymbolID: symbolID,
		Start:    s,
		End:      lines.pos(end),
		Text:     text,
		Hash:     HashText(text),
	}
}

// lineIndex maps byte offsets of one file to positions.
type lineIndex struct {
	src    []byte
	starts []int // starts[i] is the offset of line i+1; a last line without a newline counts
}

func newLineIndex(src []byte) *lineIndex {
	starts := []int{0}
	for i, c := range src {
		if c == '\n' && i+1 < len(src) {
			starts = append(starts, i+1)
		}
	}
	return &lineIndex{src: src, starts: starts}
}

// count is the number of lines.
func (l *lineIndex) count() int { return len(l.starts) }

// start is the offset of the 0-based line i.
func (l *lineIndex) start(i int) int { return l.starts[i] }

// next is the offset just past line i and its newline.
func (l *lineIndex) next(i int) int {
	if i+1 < len(l.starts) {
		return l.starts[i+1]
	}
	return len(l.src)
}

// end is the offset of the end of line i, its newline excluded.
func (l *lineIndex) end(i int) int {
	e := l.next(i)
	if e > l.starts[i] && l.src[e-1] == '\n' {
		e--
	}
	return e
}

// line is the 0-based line holding offset.
func (l *lineIndex) line(offset int) int {
	return sort.Search(len(l.starts), func(i int) bool { return l.starts[i] > offset }) - 1
}

// pos is the position of offset.
func (l *lineIndex) pos(offset int) Pos {
	offset = max(0, min(offset, len(l.src)))
	i := l.line(offset)
	return Pos{Line: i + 1, Column: offset - l.starts[i], Offset: offset}
}

// lineStartBefore is the start of offset's line when only blanks precede
// offset on it, and offset itself otherwise.
func (l *lineIndex) lineStartBefore(offset int) int {
	s := l.starts[l.line(offset)]
	if len(bytes.TrimLeft(l.src[s:offset], " \t")) == 0 {
		return s
	}
	return offset
}
