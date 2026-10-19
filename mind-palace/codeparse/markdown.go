package codeparse

import (
	"bytes"
	"regexp"
)

// Markdown files are chunked by heading (plan 0012): one section chunk per
// heading with the body up to the next heading of any level, and one for
// the text before the first heading. A section over MaxChunkTokens is cut
// at blank lines into parts that each repeat the heading line.

var (
	atxHeading    = regexp.MustCompile(`^ {0,3}#{1,6}(?:[ \t]|$)`)
	setextHeading = regexp.MustCompile(`^ {0,3}=+[ \t]*$`)
	fence         = regexp.MustCompile("^ {0,3}(```|~~~)")
)

// MarkdownFacts are the facts of a Markdown file: Lang Markdown and its
// section chunks.
func MarkdownFacts(content []byte) Facts {
	return Facts{Lang: Markdown, Chunks: markdownChunks(content)}
}

func markdownChunks(content []byte) []Chunk {
	if len(content) == 0 || isBinary(content) {
		return nil
	}
	lines := newLineIndex(content)
	n := lines.count()
	text := func(i int) []byte { return content[lines.start(i):lines.end(i)] }

	// heads are the 0-based lines starting a section, headings[i] whether
	// heads[i] is a heading (the first section may be the preamble).
	var heads []int
	var headings []bool
	inFence := false
	var fenceMark []byte
	for i := 0; i < n; i++ {
		line := text(i)
		if m := fence.FindSubmatch(line); m != nil {
			switch {
			case !inFence:
				inFence, fenceMark = true, m[1]
			case bytes.Equal(m[1], fenceMark):
				inFence = false
			}
			continue
		}
		if inFence {
			continue
		}
		switch {
		case atxHeading.Match(line):
			heads, headings = append(heads, i), append(headings, true)
		case i > 0 && setextHeading.Match(line) && len(bytes.TrimSpace(text(i-1))) > 0 &&
			(len(heads) == 0 || heads[len(heads)-1] != i-1):
			heads, headings = append(heads, i-1), append(headings, true)
		}
	}
	if len(heads) == 0 || heads[0] > 0 {
		heads, headings = append([]int{0}, heads...), append([]bool{false}, headings...)
	}

	var chunks []Chunk
	for k, first := range heads {
		last := n - 1
		if k+1 < len(heads) {
			last = heads[k+1] - 1
		}
		for last >= first && len(bytes.TrimSpace(text(last))) == 0 {
			last--
		}
		if last < first {
			continue
		}
		heading := ""
		if headings[k] {
			heading = string(text(first))
			if first+1 <= last && setextHeading.Match(text(first+1)) {
				heading += "\n" + string(text(first+1))
			}
		}
		chunks = append(chunks, sectionChunks(lines, first, last, heading)...)
		if len(chunks) >= MaxChunks {
			return chunks[:MaxChunks]
		}
	}
	return chunks
}

// sectionChunks cuts the section on lines first..last into chunks of at
// most MaxChunkTokens, at blank lines where it can, every part after the
// first starting with the heading.
func sectionChunks(lines *lineIndex, first, last int, heading string) []Chunk {
	start, end := lines.start(first), lines.end(last)
	var cuts []int
	for i := first + 1; i <= last; i++ {
		if len(bytes.TrimSpace(lines.src[lines.start(i-1):lines.end(i-1)])) == 0 {
			cuts = append(cuts, lines.start(i))
		}
	}
	prefix := ""
	if heading != "" {
		prefix = heading + "\n\n"
	}
	parts := splitSpan(lines, start, end, cuts, prefix)
	out := make([]Chunk, 0, len(parts))
	for i, p := range parts {
		out = append(out, newChunk(lines, ChunkSection, "", first+1, i+1, p.start, p.end, p.text))
	}
	return out
}
