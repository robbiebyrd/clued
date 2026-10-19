package codeparse

import (
	"bytes"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	ts "github.com/odvcencio/gotreesitter"
)

// Structural chunks (plan 0012, "Chunks follow structure"): one chunk per
// top-level definition with its doc comment; a definition over
// MaxChunkTokens is cut at statement boundaries into parts that each
// repeat its signature; the code between definitions (package clauses,
// imports, top-level variables and statements) is cut into windows, so
// every line of a parsed file is in some chunk.

// MaxChunkTokens bounds the text of one chunk, estimated as its length over
// four (the rule session/query.Tokens uses).
const MaxChunkTokens = 1500

const (
	bytesPerToken = 4
	maxChunkBytes = MaxChunkTokens * bytesPerToken
	// minPartBytes is the least room a part keeps for code after its
	// repeated signature, so a huge signature cannot stall the split.
	minPartBytes = 256
	// minGapBytes is the least text the code between definitions needs
	// for a window of its own: a lone "type (" or closing brace has none.
	minGapBytes = 16
	// maxCutDepth bounds how deep the splitter descends into nested
	// statements looking for boundaries.
	maxCutDepth = 16
)

// Kinds a chunk is never made for (their text is in the windows between
// chunks) and kinds whose children are chunked instead of themselves.
var (
	unchunkedKinds   = map[string]bool{KindVar: true, KindConst: true, KindField: true, KindProperty: true}
	transparentKinds = map[string]bool{KindModule: true, KindNamespace: true, KindPackage: true}
)

// span is one part of a split: the file's bytes [start, end) and the text
// it is retrieved as.
type span struct {
	start, end int
	text       string
}

// splitSpan cuts src[start:end] into parts of at most MaxChunkTokens. Every
// part after the first is prefix followed by its bytes. Parts end at the
// furthest of cuts (sorted line starts, the preferred boundaries) that
// fits, else at the furthest line start that fits, else inside a line at a
// rune boundary. A part ending at a line start leaves its newline out.
func splitSpan(lines *lineIndex, start, end int, cuts []int, prefix string) []span {
	src := lines.src
	var parts []span
	for s := start; ; {
		pre := ""
		if len(parts) > 0 {
			pre = prefix
		}
		room := max(maxChunkBytes-len(pre), minPartBytes)
		if end-s <= room {
			return append(parts, span{s, end, pre + string(src[s:end])})
		}
		limit := s + room
		c := -1
		if i := sort.SearchInts(cuts, limit+1) - 1; i >= 0 && cuts[i] > s {
			c = cuts[i]
		}
		if c < 0 {
			if ls := lines.start(lines.line(limit)); ls > s {
				c = ls
			}
		}
		if c < 0 {
			c = limit
			for c > s+1 && !utf8.RuneStart(src[c]) {
				c--
			}
		}
		e := c
		if e > s && src[e-1] == '\n' {
			e--
		}
		parts = append(parts, span{s, e, pre + string(src[s:e])})
		s = c
	}
}

// chunkSymbol is what the chunker needs of one definition.
type chunkSymbol struct {
	id        string
	kind      string
	signature string
	node      *ts.Node
	start     int // the chunk's first byte: the doc comment's, else the definition's
	end       int
	container int // index of the enclosing definition by position, -1 at top level
}

// codeChunks returns the chunks of a parsed file: one per top-level
// definition (or its parts) and windows over the code between them, in
// source order, at most MaxChunks.
func codeChunks(lines *lineIndex, lang *ts.Language, syms []chunkSymbol) []Chunk {
	var chunks []Chunk
	chunked := make([]bool, len(syms))
	lastEnd := -1
	var covered [][2]int // line ranges of the definition chunks
	for i, s := range syms {
		if !chunkable(syms, i) || s.end <= lastEnd {
			continue
		}
		chunked[i] = true
		lastEnd = s.end
		start := lines.lineStartBefore(s.start)
		covered = append(covered, [2]int{lines.line(start), lines.line(max(start, s.end-1))})
		if s.end-start <= maxChunkBytes {
			chunks = append(chunks, newChunk(lines, ChunkSymbol, s.id, 0, 1, start, s.end, string(lines.src[start:s.end])))
			continue
		}
		var cuts []int
		collectCuts(lines, lang, s.node, s.end, 0, &cuts)
		sort.Ints(cuts)
		prefix := ""
		if s.signature != "" {
			prefix = s.signature + "\n"
		}
		for p, part := range splitSpan(lines, start, s.end, compactInts(cuts), prefix) {
			chunks = append(chunks, newChunk(lines, ChunkPart, s.id, 0, p+1, part.start, part.end, part.text))
		}
	}
	chunks = append(chunks, gapChunks(lines, covered)...)
	sort.SliceStable(chunks, func(i, j int) bool { return chunks[i].Start.Offset < chunks[j].Start.Offset })
	if len(chunks) > MaxChunks {
		chunks = chunks[:MaxChunks]
	}
	// A window's ID (L<line>#n) could only meet a symbol's in a language
	// without packages whose symbol is named like one; keep IDs unique.
	seen := make(map[string]int, len(chunks))
	for i := range chunks {
		id := chunks[i].ID
		if seen[id]++; seen[id] > 1 {
			chunks[i].ID = id + "~" + strconv.Itoa(seen[id])
		}
	}
	return chunks
}

// chunkable reports whether syms[i] gets its own chunk: a kind that is
// chunked, inside nothing but namespaces and modules.
func chunkable(syms []chunkSymbol, i int) bool {
	if unchunkedKinds[syms[i].kind] || transparentKinds[syms[i].kind] {
		return false
	}
	for c := syms[i].container; c >= 0; c = syms[c].container {
		if !transparentKinds[syms[c].kind] {
			return false
		}
	}
	return true
}

// collectCuts adds the line starts of n's statements (the named children
// of its body, or of n itself when it has none), descending into those too
// large for one chunk, so a large definition is cut between statements.
func collectCuts(lines *lineIndex, lang *ts.Language, n *ts.Node, limit, depth int, cuts *[]int) {
	if n == nil || depth > maxCutDepth {
		return
	}
	parent := n
	if depth == 0 {
		if body := n.ChildByFieldName("body", lang); body != nil {
			parent = body
		}
	}
	for parent.NamedChildCount() == 1 && parent.NamedChild(0).NamedChildCount() > 0 {
		parent = parent.NamedChild(0)
	}
	prevComment := -1 // the last line of the comment just before, or -1
	for i := 0; i < parent.NamedChildCount(); i++ {
		u := parent.NamedChild(i)
		start, end := int(u.StartByte()), int(u.EndByte())
		if start >= limit {
			break
		}
		// A statement stays with the comments on the lines just above it.
		attached := prevComment >= 0 && lines.line(start) == prevComment+1
		if ls := lines.lineStartBefore(start); !attached && ls == lines.start(lines.line(start)) {
			*cuts = append(*cuts, ls)
		}
		prevComment = -1
		if strings.Contains(u.Type(lang), "comment") && end > start {
			prevComment = lines.line(end - 1)
		}
		if end-start > maxChunkBytes-minPartBytes {
			collectCuts(lines, lang, u, limit, depth+1, cuts)
		}
	}
}

// gapChunks returns windows over the lines no definition chunk covers,
// cut at blank lines, blank lines at either end of a gap left out, and
// gaps of less than minGapBytes left out.
func gapChunks(lines *lineIndex, covered [][2]int) []Chunk {
	var chunks []Chunk
	blank := func(i int) bool { return len(bytes.TrimSpace(lines.src[lines.start(i):lines.end(i)])) == 0 }
	emit := func(first, last int) {
		for first <= last && blank(first) {
			first++
		}
		for last >= first && blank(last) {
			last--
		}
		if first > last || len(bytes.TrimSpace(lines.src[lines.start(first):lines.end(last)])) < minGapBytes {
			return
		}
		var cuts []int
		for i := first + 1; i <= last; i++ {
			if blank(i - 1) {
				cuts = append(cuts, lines.start(i))
			}
		}
		for p, part := range splitSpan(lines, lines.start(first), lines.end(last), cuts, "") {
			chunks = append(chunks, newChunk(lines, ChunkWindow, "", first+1, p+1, part.start, part.end, part.text))
		}
	}
	next := 0
	for _, c := range covered {
		if c[0] > next {
			emit(next, c[0]-1)
		}
		next = max(next, c[1]+1)
	}
	if next < lines.count() {
		emit(next, lines.count()-1)
	}
	return chunks
}

func compactInts(s []int) []int {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}
