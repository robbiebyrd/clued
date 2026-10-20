// Package falkor is the FalkorDB codegraph adapter. Each project's graph is
// one FalkorDB graph whose Redis key is the graph key (the project
// namespace), so tenancy is physical. It talks to FalkorDB over the Redis
// protocol with a small client of its own (standard library only, no cgo):
// every query is a constant Cypher text sent with GRAPH.QUERY or
// GRAPH.RO_QUERY and its values travel as query parameters (the CYPHER
// k=v prefix, encoded as literals), never spliced into the text.
//
// The schema is codegraph's (schema.go): Layer, File, Package, Symbol and
// Chunk nodes keyed by (layer, id/path/name), structural CONTAINS,
// IN_PACKAGE and IMPORTS edges written by ApplyFile, and resolved CALLS,
// REFERENCES, IMPLEMENTS and EXTENDS edges carrying the layer that resolved
// them. The range indexes and the full-text index over a symbol's name,
// signature and doc comment (FalkorDB's db.idx.fulltext procedures, without
// stemming and weighted 3, 2 and 1) are created by PutLayer when missing, so
// a dropped graph gets them back when it is rebuilt (a full-text query on a
// graph without its index finds nothing rather than failing).
//
// Visibility is filtered in the queries (a file's layer in the view, not a
// tombstone, no view layer in its hiddenBy); the rules the memory adapter
// applies in Go on top of that (the highest layer's copy of an id answers,
// orders, limits, breadth-first walks) are applied here the same way, so
// both adapters pass codegraph/codegraphtest with identical answers.
//
// Full text differs from the memory adapter in one known way: FalkorDB folds
// the case of ASCII letters only, so a query matches other scripts' letters
// in the case they were written in.
package falkor

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/robbiebyrd/clued/mind-palace/codegraph"
)

// Connection settings.
const (
	// DefaultAddr is the address a URL without a host names.
	DefaultAddr = "127.0.0.1:6379"
	// maxConns caps the connections one Opener holds open at once.
	maxConns = 32
	// maxIdle is how many idle connections the pool keeps.
	maxIdle = 8
	// dialTimeout bounds connecting when the context has no deadline.
	dialTimeout = 5 * time.Second
	// maxBulk refuses a reply bulk string larger than this.
	maxBulk = 512 << 20
)

// Opener opens graphs on one FalkorDB server. It holds a small connection
// pool; graphs opened from it share the pool and, per key, a write lock, so
// the multi-query writes of one process never interleave.
type Opener struct {
	addr     string
	tls      *tls.Config
	user     string
	password string
	db       int

	idle   chan *conn
	sem    chan struct{}
	closed atomic.Bool

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

var _ codegraph.Opener = (*Opener)(nil)

// New returns an Opener for a FalkorDB URL: redis://[user:password@]host:port[/db],
// or rediss:// for TLS. It does not connect; the first query does (see Ping).
func New(rawURL string) (*Opener, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("%w: falkordb url: %v", codegraph.ErrInvalid, err)
	}
	o := &Opener{
		addr:  DefaultAddr,
		idle:  make(chan *conn, maxIdle),
		sem:   make(chan struct{}, maxConns),
		locks: make(map[string]*sync.Mutex),
	}
	switch u.Scheme {
	case "redis":
	case "rediss":
		o.tls = &tls.Config{MinVersion: tls.VersionTLS12}
	default:
		return nil, fmt.Errorf("%w: falkordb url: scheme %q, want redis or rediss", codegraph.ErrInvalid, u.Scheme)
	}
	if h := u.Host; h != "" {
		if u.Port() == "" {
			h = net.JoinHostPort(u.Hostname(), "6379")
		}
		o.addr = h
	}
	if o.tls != nil {
		host, _, _ := net.SplitHostPort(o.addr)
		o.tls.ServerName = host
	}
	if u.User != nil {
		o.password, _ = u.User.Password()
		o.user = u.User.Username()
		if _, set := u.User.Password(); !set {
			// redis://secret@host: the user part is the password.
			o.user, o.password = "", u.User.Username()
		}
	}
	if p := strings.Trim(u.Path, "/"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("%w: falkordb url: database %q", codegraph.ErrInvalid, p)
		}
		o.db = n
	}
	return o, nil
}

// Open returns the graph named key. It sends nothing: the graph and its
// indexes are created by the first PutLayer.
func (o *Opener) Open(ctx context.Context, key string) (codegraph.Graph, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := codegraph.ValidKey(key); err != nil {
		return nil, err
	}
	if o.closed.Load() {
		return nil, fmt.Errorf("%w: opener closed", codegraph.ErrUnavailable)
	}
	o.mu.Lock()
	l, ok := o.locks[key]
	if !ok {
		l = &sync.Mutex{}
		o.locks[key] = l
	}
	o.mu.Unlock()
	return &Graph{o: o, key: key, wmu: l}, nil
}

// Ping checks that the server answers.
func (o *Opener) Ping(ctx context.Context) error {
	_, err := o.do(ctx, "PING")
	return err
}

// Close closes the pool's connections; graphs opened from it stop working.
func (o *Opener) Close() error {
	if o.closed.Swap(true) {
		return nil
	}
	for {
		select {
		case c := <-o.idle:
			c.close()
		default:
			return nil
		}
	}
}

// ---- Redis protocol ----

// conn is one RESP2 connection.
type conn struct {
	nc net.Conn
	r  *bufio.Reader
	w  *bufio.Writer
}

func (c *conn) close() { _ = c.nc.Close() }

// respError is an error reply from the server.
type respError string

func (e respError) Error() string { return string(e) }

func (o *Opener) dial(ctx context.Context) (*conn, error) {
	d := net.Dialer{Timeout: dialTimeout}
	nc, err := d.DialContext(ctx, "tcp", o.addr)
	if err != nil {
		return nil, err
	}
	if o.tls != nil {
		tc := tls.Client(nc, o.tls)
		if err := tc.HandshakeContext(ctx); err != nil {
			_ = nc.Close()
			return nil, err
		}
		nc = tc
	}
	c := &conn{nc: nc, r: bufio.NewReader(nc), w: bufio.NewWriter(nc)}
	// Each step names itself with a constant so an error never quotes
	// its arguments (the password).
	type step struct {
		name string
		args []string
	}
	var setup []step
	switch {
	case o.password != "" && o.user != "":
		setup = append(setup, step{"AUTH", []string{"AUTH", o.user, o.password}})
	case o.password != "":
		setup = append(setup, step{"AUTH", []string{"AUTH", o.password}})
	}
	if o.db != 0 {
		setup = append(setup, step{"SELECT", []string{"SELECT", strconv.Itoa(o.db)}})
	}
	for _, st := range setup {
		reply, err := c.do(ctx, st.args...)
		if err == nil {
			if e, ok := reply.(respError); ok {
				err = fmt.Errorf("%s: %s", st.name, e)
			}
		}
		if err != nil {
			c.close()
			return nil, fmt.Errorf("falkor: %s failed: %w", st.name, err)
		}
	}
	return c, nil
}

// do sends one command and reads its reply. An I/O error leaves the
// connection unusable; an error reply comes back as a respError value.
func (c *conn) do(ctx context.Context, args ...string) (any, error) {
	dl, _ := ctx.Deadline()
	if err := c.nc.SetDeadline(dl); err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = c.nc.SetDeadline(time.Unix(1, 0)) })
	defer stop()
	fmt.Fprintf(c.w, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(c.w, "$%d\r\n", len(a))
		c.w.WriteString(a)
		c.w.WriteString("\r\n")
	}
	if err := c.w.Flush(); err != nil {
		return nil, err
	}
	return readReply(c.r)
}

func readLine(r *bufio.Reader) ([]byte, error) {
	line, err := r.ReadSlice('\n')
	if err != nil {
		return nil, err
	}
	if len(line) < 3 || line[len(line)-2] != '\r' {
		return nil, fmt.Errorf("falkor: malformed reply line %q", line)
	}
	return line[:len(line)-2], nil
}

func readReply(r *bufio.Reader) (any, error) {
	line, err := readLine(r)
	if err != nil {
		return nil, err
	}
	switch line[0] {
	case '+':
		return string(line[1:]), nil
	case '-':
		return respError(line[1:]), nil
	case ':':
		return strconv.ParseInt(string(line[1:]), 10, 64)
	case '$':
		n, err := strconv.Atoi(string(line[1:]))
		if err != nil {
			return nil, fmt.Errorf("falkor: bulk length %q", line[1:])
		}
		if n < 0 {
			return nil, nil
		}
		if n > maxBulk {
			return nil, fmt.Errorf("falkor: bulk string of %d bytes", n)
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		return string(buf[:n]), nil
	case '*':
		n, err := strconv.Atoi(string(line[1:]))
		if err != nil {
			return nil, fmt.Errorf("falkor: array length %q", line[1:])
		}
		if n < 0 {
			return nil, nil
		}
		out := make([]any, n)
		for i := range out {
			if out[i], err = readReply(r); err != nil {
				return nil, err
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("falkor: unexpected reply type %q", line[0])
}

// do runs one command on a pooled connection. A connection or I/O failure
// is ErrUnavailable (or the context's error when it ended first).
func (o *Opener) do(ctx context.Context, args ...string) (any, error) {
	if o.closed.Load() {
		return nil, fmt.Errorf("%w: opener closed", codegraph.ErrUnavailable)
	}
	select {
	case o.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-o.sem }()
	var c *conn
	select {
	case c = <-o.idle:
	default:
	}
	if c == nil {
		var err error
		if c, err = o.dial(ctx); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("%w: %v", codegraph.ErrUnavailable, err)
		}
	}
	reply, err := c.do(ctx, args...)
	if err != nil {
		c.close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", codegraph.ErrUnavailable, err)
	}
	if o.closed.Load() {
		c.close()
	} else {
		select {
		case o.idle <- c:
		default:
			c.close()
		}
	}
	if e, ok := reply.(respError); ok {
		return nil, e
	}
	return reply, nil
}

// ---- query parameters ----

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// params are a query's parameters; buildQuery encodes them as Cypher
// literals in the CYPHER prefix, which is how FalkorDB receives parameters.
type params map[string]any

// buildQuery prefixes the constant query text with its parameters.
func buildQuery(q string, p params) (string, error) {
	if len(p) == 0 {
		return q, nil
	}
	names := make([]string, 0, len(p))
	for k := range p {
		names = append(names, k)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("CYPHER")
	for _, k := range names {
		if !identRe.MatchString(k) {
			return "", fmt.Errorf("falkor: parameter name %q", k)
		}
		b.WriteByte(' ')
		b.WriteString(k)
		b.WriteByte('=')
		if err := writeLiteral(&b, p[k]); err != nil {
			return "", err
		}
	}
	b.WriteByte(' ')
	b.WriteString(q)
	return b.String(), nil
}

// writeLiteral encodes a parameter value. Strings are double-quoted with
// backslash escapes; NUL (which the server's parser would end the string
// at) and invalid UTF-8 become U+FFFD.
func writeLiteral(b *strings.Builder, v any) error {
	switch v := v.(type) {
	case nil:
		b.WriteString("null")
	case string:
		writeString(b, v)
	case bool:
		b.WriteString(strconv.FormatBool(v))
	case int:
		b.WriteString(strconv.Itoa(v))
	case int64:
		b.WriteString(strconv.FormatInt(v, 10))
	case float64:
		f := strconv.FormatFloat(v, 'f', -1, 64)
		if !strings.ContainsAny(f, ".eE") {
			f += ".0"
		}
		b.WriteString(f)
	case []string:
		b.WriteByte('[')
		for i, s := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			writeString(b, s)
		}
		b.WriteByte(']')
	case []any:
		b.WriteByte('[')
		for i, e := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeLiteral(b, e); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case []map[string]any:
		b.WriteByte('[')
		for i, e := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeLiteral(b, e); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if !identRe.MatchString(k) {
				return fmt.Errorf("falkor: map key %q", k)
			}
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteByte('`')
			b.WriteString(k)
			b.WriteString("`:")
			if err := writeLiteral(b, v[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		return fmt.Errorf("falkor: unsupported parameter type %T", v)
	}
	return nil
}

func writeString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size <= 1 {
			b.WriteRune(utf8.RuneError)
			i += max(size, 1)
			continue
		}
		i += size
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case 0:
			b.WriteRune(utf8.RuneError)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
}

// ---- results ----

// result is a compact GRAPH.QUERY reply decoded into Go values: nil,
// string, int64, bool, float64, []any and map[string]any.
type result struct {
	rows [][]any
}

// Compact value types.
const (
	valNull   = 1
	valString = 2
	valInt    = 3
	valBool   = 4
	valDouble = 5
	valArray  = 6
	valMap    = 10
)

func parseResult(reply any) (*result, error) {
	parts, ok := reply.([]any)
	if !ok {
		return nil, fmt.Errorf("falkor: unexpected reply %T", reply)
	}
	res := &result{}
	if len(parts) < 3 {
		return res, nil // statistics only
	}
	rows, ok := parts[1].([]any)
	if !ok {
		return nil, fmt.Errorf("falkor: unexpected rows %T", parts[1])
	}
	for _, r := range rows {
		cells, ok := r.([]any)
		if !ok {
			return nil, fmt.Errorf("falkor: unexpected row %T", r)
		}
		row := make([]any, len(cells))
		for i, c := range cells {
			v, err := decodeValue(c)
			if err != nil {
				return nil, err
			}
			row[i] = v
		}
		res.rows = append(res.rows, row)
	}
	return res, nil
}

func decodeValue(c any) (any, error) {
	pair, ok := c.([]any)
	if !ok || len(pair) != 2 {
		return nil, fmt.Errorf("falkor: unexpected value %v", c)
	}
	t, _ := pair[0].(int64)
	v := pair[1]
	switch t {
	case valNull:
		return nil, nil
	case valString:
		s, _ := v.(string)
		return s, nil
	case valInt:
		n, _ := v.(int64)
		return n, nil
	case valBool:
		s, _ := v.(string)
		return s == "true", nil
	case valDouble:
		s, _ := v.(string)
		return strconv.ParseFloat(s, 64)
	case valArray:
		elems, _ := v.([]any)
		out := make([]any, len(elems))
		for i, e := range elems {
			d, err := decodeValue(e)
			if err != nil {
				return nil, err
			}
			out[i] = d
		}
		return out, nil
	case valMap:
		elems, _ := v.([]any)
		out := make(map[string]any, len(elems)/2)
		for i := 0; i+1 < len(elems); i += 2 {
			k, _ := elems[i].(string)
			d, err := decodeValue(elems[i+1])
			if err != nil {
				return nil, err
			}
			out[k] = d
		}
		return out, nil
	}
	return nil, fmt.Errorf("falkor: unsupported value type %d (return scalars, not entities)", t)
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// num converts a reply integer to int, clamping what int cannot hold
// (32-bit targets).
func num(v any) int {
	var n int64
	switch x := v.(type) {
	case int64:
		n = x
	case float64:
		if x >= math.MaxInt64 {
			n = math.MaxInt64
		} else if x <= math.MinInt64 {
			n = math.MinInt64
		} else {
			n = int64(x)
		}
	default:
		return 0
	}
	if n > math.MaxInt {
		return math.MaxInt
	}
	if n < math.MinInt {
		return math.MinInt
	}
	return int(n)
}

func flag(v any) bool {
	b, _ := v.(bool)
	return b
}

func strs(v any) []string {
	list, _ := v.([]any)
	if len(list) == 0 {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, e := range list {
		out = append(out, str(e))
	}
	return out
}

func float(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	}
	return 0
}

// ---- graph ----

// Graph is one key's graph.
type Graph struct {
	o   *Opener
	key string
	wmu *sync.Mutex // the key's write lock, shared by the Opener's handles
}

var _ codegraph.Graph = (*Graph)(nil)

// Key is the graph's key.
func (g *Graph) Key() string { return g.key }

// errEmptyKey: the graph does not exist.
var errEmptyKey = errors.New("empty graph")

// transient lists error replies that mean "try again later".
var transient = []string{"LOADING", "BUSY", "MASTERDOWN", "TRYAGAIN", "CLUSTERDOWN", "Max pending queries exceeded", "Query timed out"}

func (g *Graph) run(ctx context.Context, cmd, q string, p params) (*result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	text, err := buildQuery(q, p)
	if err != nil {
		return nil, err
	}
	reply, err := g.o.do(ctx, cmd, g.key, text, "--compact")
	if err != nil {
		var re respError
		if !errors.As(err, &re) {
			return nil, err
		}
		msg := string(re)
		if strings.Contains(msg, "empty key") {
			return nil, errEmptyKey
		}
		for _, t := range transient {
			if strings.Contains(msg, t) {
				return nil, fmt.Errorf("%w: %s", codegraph.ErrUnavailable, msg)
			}
		}
		return nil, fmt.Errorf("falkor: %s", msg)
	}
	return parseResult(reply)
}

// read runs a read-only query; a graph that does not exist reads as empty.
func (g *Graph) read(ctx context.Context, q string, p params) (*result, error) {
	res, err := g.run(ctx, "GRAPH.RO_QUERY", q, p)
	if errors.Is(err, errEmptyKey) {
		return &result{}, nil
	}
	return res, err
}

// write runs a query that may change the graph (and creates it if missing).
func (g *Graph) write(ctx context.Context, q string, p params) (*result, error) {
	return g.run(ctx, "GRAPH.QUERY", q, p)
}

// exists reports whether the graph's key exists, so writes that must not
// create a graph can skip it.
func (g *Graph) exists(ctx context.Context) (bool, error) {
	reply, err := g.o.do(ctx, "EXISTS", g.key)
	if err != nil {
		return false, err
	}
	n, _ := reply.(int64)
	return n > 0, nil
}

// ---- queries ----

// The query texts are constants (some assembled at init from the schema's
// constants); every value reaches them as a parameter.

// visibleF is the visibility rule for the file bound to f (and the nodes it
// contains): in the view, not a tombstone, hidden by no layer of the view.
const visibleF = `f.layer IN $view AND NOT coalesce(f.deleted, false) AND none(h IN coalesce(f.hiddenBy, []) WHERE h IN $view)`

// A query that finds symbols by key and then their files binds the symbols
// first and passes them on with WITH: without it FalkorDB plans the file
// match first (an index scan of every visible file) and expands each file's
// symbols, which on a large graph runs past its query timeout.

// visibleOf is visibleF for another variable.
func visibleOf(v string) string { return strings.ReplaceAll(visibleF, "f.", v+".") }

// symbolCols are a Symbol's properties (alias s); parseSymbol reads them.
const symbolCols = "s.layer, s.id, s.kind, s.name, s.qualName, s.signature, s.doc, s.path, s.start, s.`end`, s.exported, s.parent"

const nSymbolCols = 12

func symbolColsOf(v string) string { return strings.ReplaceAll(symbolCols, "s.", v+".") }

// fileCols are a File's properties (alias f); parseFile reads them.
const fileCols = "f.layer, f.path, f.sha256, f.git_oid, f.lang, f.package, f.hiddenBy, f.deleted, f.parse_error"

const nFileCols = 9

const chunkCols = "c.layer, c.id, c.kind, c.symbol, c.path, c.start, c.`end`, c.hash"

const (
	qPutLayer = `MERGE (l:Layer {id: $id})
SET l.kind = $kind, l.parent = $parent, l.owner = $owner, l.ref = $ref, l.commit = $commit, l.updated = $updated`
	qLayers  = `MATCH (l:Layer) RETURN l.id, l.kind, l.parent, l.owner, l.ref, l.commit, l.updated`
	qIndexes = `CALL db.indexes() YIELD label, types RETURN label, types`
	qFiles   = `MATCH (l:Layer {id: $layer}) OPTIONAL MATCH (f:File {layer: $layer}) RETURN ` + fileCols

	qApplyCheck = `OPTIONAL MATCH (l:Layer {id: $layer})
WITH count(l) AS layers
OPTIONAL MATCH (f:File {layer: $layer, path: $path})
WITH layers, count(f) AS files
OPTIONAL MATCH (s:Symbol) WHERE s.id IN $symbols AND s.layer = $layer AND s.path <> $path
WITH layers, files, collect(s.id) AS sids, collect(s.path) AS spaths
OPTIONAL MATCH (c:Chunk) WHERE c.id IN $chunks AND c.layer = $layer AND c.path <> $path
RETURN layers, files, sids, spaths, collect(c.id), collect(c.path)`
	qApplyClearNodes = `MATCH (f:File {layer: $layer, path: $path})-[:CONTAINS]->(n)
WHERE n:Chunk OR (n:Symbol AND NOT n.id IN $symbols)
DETACH DELETE n`
	qApplyClearFileEdges = `MATCH (f:File {layer: $layer, path: $path})-[r:IN_PACKAGE|IMPORTS]->() DELETE r`
	qApplyClearParents   = `MATCH (f:File {layer: $layer, path: $path})-[:CONTAINS]->(:Symbol)-[r:CONTAINS]->(:Symbol) DELETE r`
	qApplyWrite          = `MATCH (l:Layer {id: $layer})
MERGE (f:File {layer: $layer, path: $path})
SET f += $file, f.hiddenBy = coalesce($hidden, f.hiddenBy, [])
MERGE (l)-[:CONTAINS]->(f)
FOREACH (p IN $symbols | MERGE (s:Symbol {layer: $layer, id: p.id}) SET s += p MERGE (f)-[:CONTAINS]->(s))
FOREACH (e IN $parents | MERGE (a:Symbol {layer: $layer, id: e.parent}) MERGE (b:Symbol {layer: $layer, id: e.id}) CREATE (a)-[:CONTAINS]->(b))
FOREACH (c IN $chunks | CREATE (n:Chunk) SET n += c CREATE (f)-[:CONTAINS]->(n))
FOREACH (c IN $symchunks | MERGE (s:Symbol {layer: $layer, id: c.symbol}) MERGE (n:Chunk {layer: $layer, id: c.id}) CREATE (s)-[:CONTAINS]->(n))
FOREACH (x IN $packages | MERGE (p:Package {layer: $layer, name: x.name}) ON CREATE SET p.kind = x.kind)
FOREACH (x IN $inpackage | MERGE (p:Package {layer: $layer, name: x}) CREATE (f)-[:IN_PACKAGE]->(p))
FOREACH (im IN $imports | MERGE (p:Package {layer: $layer, name: im.package}) CREATE (f)-[:IMPORTS {alias: im.alias}]->(p))`

	qRemoveFileNodes = `MATCH (f:File {layer: $layer, path: $path})-[:CONTAINS]->(n) DETACH DELETE n`
	qRemoveFile      = `MATCH (f:File {layer: $layer, path: $path}) DETACH DELETE f`
	qSetHidden       = `MATCH (f:File {layer: $layer, path: $path}) SET f.hiddenBy = $hidden RETURN count(f)`

	qLayerExists = `MATCH (l:Layer {id: $layer}) RETURN count(l)`
	qMissingRefs = `UNWIND $refs AS r OPTIONAL MATCH (s:Symbol {layer: r.layer, id: r.id}) WITH r, count(s) AS n WHERE n = 0 RETURN r.layer, r.id LIMIT 1`
	qClearEdges  = `UNWIND $from AS r MATCH (s:Symbol {layer: r.layer, id: r.id})-[e]->(:Symbol)
WHERE e.layer = $layer AND type(e) IN $types DELETE e`

	qDropFiles     = `MATCH (n:File {layer: $layer}) DETACH DELETE n`
	qDropSymbols   = `MATCH (n:Symbol {layer: $layer}) DETACH DELETE n`
	qDropChunks    = `MATCH (n:Chunk {layer: $layer}) DETACH DELETE n`
	qDropPackages  = `MATCH (n:Package {layer: $layer}) DETACH DELETE n`
	qDropEdges     = `MATCH (:Symbol)-[e]->(:Symbol) WHERE e.layer = $layer AND type(e) IN $types DELETE e`
	qDropHiddenBy  = `MATCH (f:File) WHERE $layer IN f.hiddenBy SET f.hiddenBy = [h IN f.hiddenBy WHERE h <> $layer]`
	qDropLayerNode = `MATCH (l:Layer {id: $layer}) DETACH DELETE l`

	qFindByName = `MATCH (s:Symbol {name: $name}) WITH s MATCH (f:File)-[:CONTAINS]->(s) WHERE ` + visibleF + `
RETURN ` + symbolCols + `, f.lang
UNION
MATCH (s:Symbol {qualName: $name}) WITH s MATCH (f:File)-[:CONTAINS]->(s) WHERE ` + visibleF + `
RETURN ` + symbolCols + `, f.lang`
	qVisibleCopies = `UNWIND $ids AS id MATCH (s:Symbol {id: id}) WITH s MATCH (f:File)-[:CONTAINS]->(s) WHERE ` + visibleF + `
RETURN s.layer, s.id`
	qSymbolByID = `MATCH (s:Symbol {id: $id}) WITH s MATCH (f:File)-[:CONTAINS]->(s) WHERE ` + visibleF + `
RETURN ` + symbolCols + `, f.lang`
	qSymbolByQual = `MATCH (s:Symbol {qualName: $qual}) WITH s MATCH (f:File)-[:CONTAINS]->(s) WHERE ` + visibleF + `
RETURN ` + symbolCols + `, f.lang`
	qChildren = `MATCH (s:Symbol {layer: $layer, parent: $id}) RETURN ` + symbolCols
	qChunks   = `MATCH (c:Chunk {layer: $layer, symbol: $id}) RETURN ` + chunkCols

	qFilesAt        = `MATCH (f:File {path: $path}) WHERE ` + visibleF + ` RETURN ` + fileCols
	qFilesInPackage = `MATCH (f:File {package: $package}) WHERE ` + visibleF + ` RETURN ` + fileCols
	qFileSymbols    = `UNWIND $files AS k MATCH (f:File {layer: k.layer, path: k.path})-[:CONTAINS]->(s:Symbol)
RETURN f.layer, f.path, ` + symbolCols
	qImportsOf = `MATCH (f:File {layer: $layer, path: $path})-[e:IMPORTS]->(p:Package)
RETURN e.alias, p.layer, p.name, p.kind`
	qPackageInView = `MATCH (p:Package {name: $name}) WHERE p.layer IN $view RETURN count(p)`
	qImporters     = `MATCH (f:File)-[e:IMPORTS]->(p:Package {name: $name}) WHERE ` + visibleF + `
RETURN ` + fileCols + `, e.alias, p.layer, p.name, p.kind`
	qVisibleLayersOf = `MATCH (s:Symbol {id: $id}) WITH s MATCH (f:File)-[:CONTAINS]->(s) WHERE ` + visibleF + ` RETURN s.layer`

	qSearch = `CALL db.idx.fulltext.queryNodes($label, $query) YIELD node, score
WITH node AS s, score
MATCH (f:File)-[:CONTAINS]->(s) WHERE ` + visibleF + `
RETURN ` + symbolCols + `, f.lang, score`
	qCreateFullText = `CALL db.idx.fulltext.createNodeIndex($options, $f0, $f1, $f2)`
	qDropFullText   = `CALL db.idx.fulltext.drop($label)`
)

// Queries assembled at init from the schema's constants (labels and edge
// types cannot be parameters).
var (
	// qHop[incoming] follows one hop of resolved edges from a frontier of
	// symbols; both return the edge's own direction (from, then to).
	qHop = map[bool]string{
		false: `UNWIND $frontier AS r
MATCH (s:Symbol {layer: r.layer, id: r.id})-[e]->(t:Symbol)
WHERE type(e) IN $types AND e.layer IN $view
WITH s, e, t
MATCH (f:File)-[:CONTAINS]->(s) MATCH (g:File)-[:CONTAINS]->(t)
WHERE ` + visibleF + ` AND ` + visibleOf("g") + `
RETURN type(e), e.layer, e.confidence, e.line, ` + symbolCols + `, ` + symbolColsOf("t"),
		true: `UNWIND $frontier AS r
MATCH (s:Symbol {layer: r.layer, id: r.id})<-[e]-(t:Symbol)
WHERE type(e) IN $types AND e.layer IN $view
WITH s, e, t
MATCH (f:File)-[:CONTAINS]->(s) MATCH (g:File)-[:CONTAINS]->(t)
WHERE ` + visibleF + ` AND ` + visibleOf("g") + `
RETURN type(e), e.layer, e.confidence, e.line, ` + symbolColsOf("t") + `, ` + symbolCols,
	}
	// qCreateEdges creates one resolved edge type's edges.
	qCreateEdges = map[codegraph.EdgeType]string{}
	// qCreateRange creates the range index of one label's property.
	qCreateRange = map[[2]string]string{}
)

// fullTextWeights weigh a full-text hit by field, as the memory adapter
// scores them.
var fullTextWeights = map[string]float64{
	codegraph.PropName: 3, codegraph.PropSignature: 2, codegraph.PropDoc: 1,
}

var labelRe = regexp.MustCompile(`^[A-Za-z_]+$`)

func init() {
	for _, t := range codegraph.ResolvedEdgeTypes {
		if !labelRe.MatchString(string(t)) {
			panic("falkor: edge type " + string(t))
		}
		qCreateEdges[t] = `UNWIND $edges AS e
MATCH (a:Symbol {layer: e.fl, id: e.fi}) MATCH (b:Symbol {layer: e.tl, id: e.ti})
CREATE (a)-[:` + string(t) + ` {layer: e.layer, confidence: e.confidence, count: e.count, line: e.line}]->(b)`
	}
	for _, ix := range codegraph.Indexes {
		if ix.Kind != codegraph.IndexRange {
			continue
		}
		for _, p := range ix.Props {
			if !labelRe.MatchString(ix.Label) || !labelRe.MatchString(p) {
				panic("falkor: index " + ix.Label + "." + p)
			}
			qCreateRange[[2]string{ix.Label, p}] = `CREATE INDEX FOR (n:` + ix.Label + `) ON (n.` + p + `)`
		}
	}
}

func resolvedTypes() []string {
	out := make([]string, len(codegraph.ResolvedEdgeTypes))
	for i, t := range codegraph.ResolvedEdgeTypes {
		out[i] = string(t)
	}
	return out
}

func viewParam(v codegraph.View) []string { return append([]string(nil), v...) }

// ---- row parsing ----

func parseSymbol(row []any, at int) codegraph.Symbol {
	r := row[at:]
	return codegraph.Symbol{
		Layer: str(r[0]), ID: str(r[1]), Kind: str(r[2]), Name: str(r[3]), QualName: str(r[4]),
		Signature: str(r[5]), Doc: str(r[6]), Path: str(r[7]), Start: num(r[8]), End: num(r[9]),
		Exported: flag(r[10]), Parent: str(r[11]),
	}
}

func parseFile(row []any, at int) codegraph.File {
	r := row[at:]
	return codegraph.File{
		Layer: str(r[0]), Path: str(r[1]), SHA256: str(r[2]), GitOID: str(r[3]), Lang: str(r[4]),
		Package: str(r[5]), HiddenBy: strs(r[6]), Deleted: flag(r[7]), ParseError: flag(r[8]),
	}
}

func parseChunk(row []any) codegraph.Chunk {
	return codegraph.Chunk{
		Layer: str(row[0]), ID: str(row[1]), Kind: str(row[2]), Symbol: str(row[3]), Path: str(row[4]),
		Start: num(row[5]), End: num(row[6]), Hash: str(row[7]),
	}
}

func symbolProps(s codegraph.Symbol) map[string]any {
	return map[string]any{
		codegraph.PropLayer: s.Layer, codegraph.PropID: s.ID, codegraph.PropKind: s.Kind,
		codegraph.PropName: s.Name, codegraph.PropQualName: s.QualName,
		codegraph.PropSignature: s.Signature, codegraph.PropDoc: s.Doc, codegraph.PropPath: s.Path,
		codegraph.PropStart: s.Start, codegraph.PropEnd: s.End, codegraph.PropExported: s.Exported,
		codegraph.PropParent: s.Parent,
	}
}

func chunkProps(c codegraph.Chunk) map[string]any {
	return map[string]any{
		codegraph.PropLayer: c.Layer, codegraph.PropID: c.ID, codegraph.PropKind: c.Kind,
		codegraph.PropSymbol: c.Symbol, codegraph.PropPath: c.Path, codegraph.PropStart: c.Start,
		codegraph.PropEnd: c.End, codegraph.PropHash: c.Hash,
	}
}

// ---- writes ----

// PutLayer creates or updates a layer node, and creates the graph's indexes
// when they are missing (a new or dropped graph).
func (g *Graph) PutLayer(ctx context.Context, l codegraph.Layer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := l.Validate(); err != nil {
		return err
	}
	g.wmu.Lock()
	defer g.wmu.Unlock()
	updated := ""
	if !l.Updated.IsZero() {
		updated = l.Updated.UTC().Format(time.RFC3339Nano)
	}
	if _, err := g.write(ctx, qPutLayer, params{
		"id": l.ID, "kind": l.Kind, "parent": l.Parent, "owner": l.Owner,
		"ref": l.Ref, "commit": l.Commit, "updated": updated,
	}); err != nil {
		return err
	}
	return g.ensureIndexes(ctx)
}

// ensureIndexes creates the indexes of codegraph.Indexes the graph lacks.
func (g *Graph) ensureIndexes(ctx context.Context) error {
	res, err := g.read(ctx, qIndexes, nil)
	if err != nil {
		return err
	}
	have := map[[2]string]map[string]bool{} // (label, prop) → index types
	for _, row := range res.rows {
		label := str(row[0])
		types, _ := row[1].(map[string]any)
		for prop, ts := range types {
			k := [2]string{label, prop}
			if have[k] == nil {
				have[k] = map[string]bool{}
			}
			for _, t := range strs(ts) {
				have[k][strings.ToUpper(t)] = true
			}
		}
	}
	for _, ix := range codegraph.Indexes {
		switch ix.Kind {
		case codegraph.IndexRange:
			for _, p := range ix.Props {
				if have[[2]string{ix.Label, p}]["RANGE"] {
					continue
				}
				if _, err := g.write(ctx, qCreateRange[[2]string{ix.Label, p}], nil); err != nil &&
					!strings.Contains(err.Error(), "already indexed") {
					return err
				}
			}
		case codegraph.IndexFullText:
			missing, partial := 0, false
			for _, p := range ix.Props {
				if !have[[2]string{ix.Label, p}]["FULLTEXT"] {
					missing++
				}
			}
			if missing == 0 {
				continue
			}
			partial = missing < len(ix.Props)
			if partial {
				if _, err := g.write(ctx, qDropFullText, params{"label": ix.Label}); err != nil {
					return err
				}
			}
			if len(ix.Props) != 3 {
				return fmt.Errorf("falkor: full-text index over %d fields", len(ix.Props))
			}
			p := params{"options": map[string]any{"label": ix.Label}}
			for i, prop := range ix.Props {
				w := fullTextWeights[prop]
				if w == 0 {
					w = 1
				}
				p["f"+strconv.Itoa(i)] = map[string]any{"field": prop, "nostem": true, "weight": w}
			}
			if _, err := g.write(ctx, qCreateFullText, p); err != nil {
				return err
			}
		}
	}
	return nil
}

// Layers lists the layers ordered by id.
func (g *Graph) Layers(ctx context.Context) ([]codegraph.Layer, error) {
	res, err := g.read(ctx, qLayers, nil)
	if err != nil {
		return nil, err
	}
	out := make([]codegraph.Layer, 0, len(res.rows))
	for _, r := range res.rows {
		l := codegraph.Layer{ID: str(r[0]), Kind: str(r[1]), Parent: str(r[2]), Owner: str(r[3]), Ref: str(r[4]), Commit: str(r[5])}
		if u := str(r[6]); u != "" {
			t, err := time.Parse(time.RFC3339Nano, u)
			if err != nil {
				return nil, fmt.Errorf("falkor: layer %s updated %q: %v", l.ID, u, err)
			}
			l.Updated = t
		}
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Files lists one layer's files ordered by path, tombstones included.
func (g *Graph) Files(ctx context.Context, layer string) ([]codegraph.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := codegraph.ParseLayerID(layer); err != nil {
		return nil, err
	}
	res, err := g.read(ctx, qFiles, params{"layer": layer})
	if err != nil {
		return nil, err
	}
	if len(res.rows) == 0 {
		return nil, fmt.Errorf("%w: layer %q", codegraph.ErrNotFound, layer)
	}
	out := []codegraph.File{}
	for _, r := range res.rows {
		if r[1] == nil {
			continue // the layer has no files
		}
		out = append(out, parseFile(r, 0))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// ApplyFile replaces what the file's layer holds for its path with ff.
func (g *Graph) ApplyFile(ctx context.Context, ff codegraph.FileFacts) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ff.Validate(); err != nil {
		return err
	}
	f := ff.File
	symIDs := make([]string, len(ff.Symbols))
	for i, s := range ff.Symbols {
		symIDs[i] = s.ID
	}
	chunkIDs := make([]string, len(ff.Chunks))
	for i, c := range ff.Chunks {
		chunkIDs[i] = c.ID
	}
	base := params{"layer": f.Layer, "path": f.Path}
	with := func(extra params) params {
		p := params{}
		for k, v := range base {
			p[k] = v
		}
		for k, v := range extra {
			p[k] = v
		}
		return p
	}

	g.wmu.Lock()
	defer g.wmu.Unlock()
	res, err := g.read(ctx, qApplyCheck, with(params{"symbols": symIDs, "chunks": chunkIDs}))
	if err != nil {
		return err
	}
	if len(res.rows) == 0 || num(res.rows[0][0]) == 0 {
		return fmt.Errorf("%w: %q", codegraph.ErrUnknownLayer, f.Layer)
	}
	row := res.rows[0]
	if ids, paths := strs(row[2]), strs(row[3]); len(ids) > 0 {
		return fmt.Errorf("%w: symbol %q of %s already belongs to %s", codegraph.ErrInvalid, ids[0], f.Path, paths[0])
	}
	if ids, paths := strs(row[4]), strs(row[5]); len(ids) > 0 {
		return fmt.Errorf("%w: chunk %q of %s already belongs to %s", codegraph.ErrInvalid, ids[0], f.Path, paths[0])
	}
	if num(row[1]) > 0 {
		for _, q := range []string{qApplyClearNodes, qApplyClearFileEdges, qApplyClearParents} {
			if _, err := g.write(ctx, q, with(params{"symbols": symIDs})); err != nil {
				return err
			}
		}
	}

	syms := make([]map[string]any, len(ff.Symbols))
	var parents []map[string]any
	for i, s := range ff.Symbols {
		syms[i] = symbolProps(s)
		if s.Parent != "" {
			parents = append(parents, map[string]any{"id": s.ID, "parent": s.Parent})
		}
	}
	chunks := make([]map[string]any, len(ff.Chunks))
	var symChunks []map[string]any
	for i, c := range ff.Chunks {
		chunks[i] = chunkProps(c)
		if c.Symbol != "" {
			symChunks = append(symChunks, map[string]any{"id": c.ID, "symbol": c.Symbol})
		}
	}
	// Packages are created once, the first mention deciding a new one's
	// kind (the file's own package first); existing ones keep theirs.
	var pkgs []map[string]any
	seen := map[string]bool{}
	addPkg := func(name, kind string) {
		if seen[name] {
			return
		}
		seen[name] = true
		if kind == "" {
			kind = codegraph.PackageKindPackage
		}
		pkgs = append(pkgs, map[string]any{"name": name, "kind": kind})
	}
	var inPkg []string
	if f.Package != "" {
		addPkg(f.Package, ff.PackageKind)
		inPkg = []string{f.Package}
	}
	imports := make([]map[string]any, len(ff.Imports))
	for i, im := range ff.Imports {
		addPkg(im.Package, im.Kind)
		imports[i] = map[string]any{"package": im.Package, "alias": im.Alias}
	}
	var hidden any
	if f.HiddenBy != nil {
		hidden = append([]string{}, f.HiddenBy...)
	}
	_, err = g.write(ctx, qApplyWrite, with(params{
		"file": map[string]any{
			codegraph.PropSHA256: f.SHA256, codegraph.PropGitOID: f.GitOID, codegraph.PropLang: f.Lang,
			codegraph.PropPackage: f.Package, codegraph.PropDeleted: f.Deleted, codegraph.PropParseErr: f.ParseError,
		},
		"hidden":    hidden,
		"symbols":   syms,
		"parents":   orEmpty(parents),
		"chunks":    chunks,
		"symchunks": orEmpty(symChunks),
		"packages":  orEmpty(pkgs),
		"inpackage": append([]string{}, inPkg...),
		"imports":   imports,
	}))
	return err
}

func orEmpty(m []map[string]any) []map[string]any {
	if m == nil {
		return []map[string]any{}
	}
	return m
}

// RemoveFile deletes the file at path in layer and what hangs off it.
func (g *Graph) RemoveFile(ctx context.Context, layer, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := (codegraph.File{Layer: layer, Path: path}).Validate(); err != nil {
		return err
	}
	g.wmu.Lock()
	defer g.wmu.Unlock()
	if ok, err := g.exists(ctx); err != nil || !ok {
		return err
	}
	p := params{"layer": layer, "path": path}
	for _, q := range []string{qRemoveFileNodes, qRemoveFile} {
		if _, err := g.write(ctx, q, p); err != nil {
			return err
		}
	}
	return nil
}

// SetHidden replaces the hiddenBy list of the file at path in layer.
func (g *Graph) SetHidden(ctx context.Context, layer, path string, hiddenBy []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := (codegraph.File{Layer: layer, Path: path}).Validate(); err != nil {
		return err
	}
	for _, h := range hiddenBy {
		if _, err := codegraph.ParseLayerID(h); err != nil {
			return err
		}
	}
	g.wmu.Lock()
	defer g.wmu.Unlock()
	notFound := fmt.Errorf("%w: file %s:%s", codegraph.ErrNotFound, layer, path)
	ok, err := g.exists(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return notFound
	}
	res, err := g.write(ctx, qSetHidden, params{"layer": layer, "path": path, "hidden": append([]string{}, hiddenBy...)})
	if err != nil {
		return err
	}
	if len(res.rows) == 0 || num(res.rows[0][0]) == 0 {
		return notFound
	}
	return nil
}

// PutEdges replaces layer's resolved edges from the sources with edges.
func (g *Graph) PutEdges(ctx context.Context, layer string, from []codegraph.NodeRef, edges []codegraph.Edge) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := codegraph.ValidateEdges(layer, from, edges); err != nil {
		return err
	}
	g.wmu.Lock()
	defer g.wmu.Unlock()
	res, err := g.read(ctx, qLayerExists, params{"layer": layer})
	if err != nil {
		return err
	}
	if len(res.rows) == 0 || num(res.rows[0][0]) == 0 {
		return fmt.Errorf("%w: %q", codegraph.ErrUnknownLayer, layer)
	}
	// One edge per (type, layer, from, to), the last one given winning.
	type key struct {
		t        codegraph.EdgeType
		from, to codegraph.NodeRef
	}
	last := map[key]int{}
	for i, e := range edges {
		last[key{e.Type, e.From, e.To}] = i
	}
	byType := map[codegraph.EdgeType][]map[string]any{}
	var refs []map[string]any
	seenRef := map[codegraph.NodeRef]bool{}
	addRef := func(r codegraph.NodeRef) {
		if !seenRef[r] {
			seenRef[r] = true
			refs = append(refs, map[string]any{"layer": r.Layer, "id": r.ID})
		}
	}
	for i, e := range edges {
		if last[key{e.Type, e.From, e.To}] != i {
			continue
		}
		addRef(e.From)
		addRef(e.To)
		byType[e.Type] = append(byType[e.Type], map[string]any{
			"fl": e.From.Layer, "fi": e.From.ID, "tl": e.To.Layer, "ti": e.To.ID,
			"layer": e.Layer, "confidence": e.Confidence, "count": e.Count, "line": e.Line,
		})
	}
	if len(refs) > 0 {
		res, err := g.read(ctx, qMissingRefs, params{"refs": refs})
		if err != nil {
			return err
		}
		if len(res.rows) > 0 {
			return fmt.Errorf("%w: symbol %s/%s", codegraph.ErrNotFound, str(res.rows[0][0]), str(res.rows[0][1]))
		}
	}
	var src []map[string]any
	seenSrc := map[codegraph.NodeRef]bool{}
	for _, r := range from {
		if !seenSrc[r] {
			seenSrc[r] = true
			src = append(src, map[string]any{"layer": r.Layer, "id": r.ID})
		}
	}
	if len(src) > 0 {
		if _, err := g.write(ctx, qClearEdges, params{"from": src, "layer": layer, "types": resolvedTypes()}); err != nil {
			return err
		}
	}
	for _, t := range codegraph.ResolvedEdgeTypes {
		if es := byType[t]; len(es) > 0 {
			if _, err := g.write(ctx, qCreateEdges[t], params{"edges": es}); err != nil {
				return err
			}
		}
	}
	return nil
}

// DropLayer removes a layer and everything it holds or resolved.
func (g *Graph) DropLayer(ctx context.Context, layer string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := codegraph.ParseLayerID(layer); err != nil {
		return err
	}
	g.wmu.Lock()
	defer g.wmu.Unlock()
	if ok, err := g.exists(ctx); err != nil || !ok {
		return err
	}
	p := params{"layer": layer}
	for _, q := range []string{qDropFiles, qDropSymbols, qDropChunks, qDropPackages} {
		if _, err := g.write(ctx, q, p); err != nil {
			return err
		}
	}
	if _, err := g.write(ctx, qDropEdges, params{"layer": layer, "types": resolvedTypes()}); err != nil {
		return err
	}
	for _, q := range []string{qDropHiddenBy, qDropLayerNode} {
		if _, err := g.write(ctx, q, p); err != nil {
			return err
		}
	}
	return nil
}

// Drop deletes the graph's key; the key can be written again afterwards.
func (g *Graph) Drop(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	g.wmu.Lock()
	defer g.wmu.Unlock()
	_, err := g.o.do(ctx, "GRAPH.DELETE", g.key)
	var re respError
	if errors.As(err, &re) && strings.Contains(string(re), "empty key") {
		return nil
	}
	if errors.As(err, &re) {
		return fmt.Errorf("falkor: %s", re)
	}
	return err
}

// ---- reads ----

// cand is a visible symbol a read found, with its file's language and,
// for full-text hits, its score.
type cand struct {
	s     codegraph.Symbol
	lang  string
	score float64
}

func ref(s codegraph.Symbol) codegraph.NodeRef { return codegraph.NodeRef{Layer: s.Layer, ID: s.ID} }

// symbolsFrom reads candidates from rows of symbolCols then f.lang (and the
// score after it when withScore), one per (layer, id).
func symbolsFrom(res *result, withScore bool) []cand {
	seen := map[codegraph.NodeRef]bool{}
	var out []cand
	for _, r := range res.rows {
		c := cand{s: parseSymbol(r, 0), lang: str(r[nSymbolCols])}
		if withScore {
			c.score = float(r[nSymbolCols+1])
		}
		if seen[ref(c.s)] {
			continue
		}
		seen[ref(c.s)] = true
		out = append(out, c)
	}
	return out
}

// highestOnly keeps the candidates that are their id's highest visible copy
// in the view: when several layers of the view hold an id, only the highest
// one is visible, whether or not it matched the read.
func (g *Graph) highestOnly(ctx context.Context, view codegraph.View, cands []cand) ([]cand, error) {
	if len(cands) == 0 {
		return cands, nil
	}
	ids := make([]string, 0, len(cands))
	seen := map[string]bool{}
	for _, c := range cands {
		if !seen[c.s.ID] {
			seen[c.s.ID] = true
			ids = append(ids, c.s.ID)
		}
	}
	res, err := g.read(ctx, qVisibleCopies, params{"ids": ids, "view": viewParam(view)})
	if err != nil {
		return nil, err
	}
	top := map[string]int{}
	for _, r := range res.rows {
		id, rank := str(r[1]), view.Rank(str(r[0]))
		if cur, ok := top[id]; !ok || rank > cur {
			top[id] = rank
		}
	}
	out := cands[:0]
	for _, c := range cands {
		if rank, ok := top[c.s.ID]; ok && rank == view.Rank(c.s.Layer) {
			out = append(out, c)
		}
	}
	return out, nil
}

func lessSymbol(a, b codegraph.Symbol) bool {
	if a.Path != b.Path {
		return a.Path < b.Path
	}
	if a.Start != b.Start {
		return a.Start < b.Start
	}
	if a.ID != b.ID {
		return a.ID < b.ID
	}
	return a.Layer < b.Layer
}

func limit[T any](s []T, n int) []T {
	if s == nil {
		s = []T{}
	}
	if n = codegraph.EffectiveLimit(n); len(s) > n {
		s = s[:n]
	}
	return s
}

// FindSymbols returns the visible symbols named q.Name, qualified-name
// matches first, each group ordered by path, start and id.
func (g *Graph) FindSymbols(ctx context.Context, view codegraph.View, q codegraph.SymbolQuery) ([]codegraph.Symbol, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := view.Validate(); err != nil {
		return nil, err
	}
	if q.Name == "" {
		return nil, fmt.Errorf("%w: find symbols needs a name", codegraph.ErrInvalid)
	}
	res, err := g.read(ctx, qFindByName, params{"name": q.Name, "view": viewParam(view)})
	if err != nil {
		return nil, err
	}
	cands, err := g.highestOnly(ctx, view, symbolsFrom(res, false))
	if err != nil {
		return nil, err
	}
	out := []codegraph.Symbol{}
	for _, c := range cands {
		if q.Filter.Match(c.s.Kind, c.lang, c.s.Path) {
			out = append(out, c.s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		qi, qj := out[i].QualName == q.Name, out[j].QualName == q.Name
		if qi != qj {
			return qi
		}
		return lessSymbol(out[i], out[j])
	})
	return limit(out, q.Limit), nil
}

// Symbol returns one visible symbol by id or qualified name.
func (g *Graph) Symbol(ctx context.Context, view codegraph.View, key codegraph.SymbolKey) (codegraph.SymbolInfo, error) {
	if err := ctx.Err(); err != nil {
		return codegraph.SymbolInfo{}, err
	}
	if err := view.Validate(); err != nil {
		return codegraph.SymbolInfo{}, err
	}
	if key.ID == "" && key.QualName == "" {
		return codegraph.SymbolInfo{}, fmt.Errorf("%w: a symbol needs an id or a qualified name", codegraph.ErrInvalid)
	}
	var (
		res *result
		err error
	)
	if key.ID != "" {
		res, err = g.read(ctx, qSymbolByID, params{"id": key.ID, "view": viewParam(view)})
	} else {
		res, err = g.read(ctx, qSymbolByQual, params{"qual": key.QualName, "view": viewParam(view)})
	}
	if err != nil {
		return codegraph.SymbolInfo{}, err
	}
	cands, err := g.highestOnly(ctx, view, symbolsFrom(res, false))
	if err != nil {
		return codegraph.SymbolInfo{}, err
	}
	if len(cands) == 0 {
		k := key.ID
		if k == "" {
			k = key.QualName
		}
		return codegraph.SymbolInfo{}, fmt.Errorf("%w: symbol %q", codegraph.ErrNotFound, k)
	}
	sort.Slice(cands, func(i, j int) bool {
		ri, rj := view.Rank(cands[i].s.Layer), view.Rank(cands[j].s.Layer)
		if ri != rj {
			return ri > rj
		}
		return lessSymbol(cands[i].s, cands[j].s)
	})
	s := cands[0].s
	info := codegraph.SymbolInfo{Symbol: s, Lang: cands[0].lang}
	p := params{"layer": s.Layer, "id": s.ID}
	kids, err := g.read(ctx, qChildren, p)
	if err != nil {
		return codegraph.SymbolInfo{}, err
	}
	for _, r := range kids.rows {
		if c := parseSymbol(r, 0); c.Path == s.Path {
			info.Children = append(info.Children, c)
		}
	}
	sort.Slice(info.Children, func(i, j int) bool { return lessSymbol(info.Children[i], info.Children[j]) })
	chunks, err := g.read(ctx, qChunks, p)
	if err != nil {
		return codegraph.SymbolInfo{}, err
	}
	for _, r := range chunks.rows {
		if c := parseChunk(r); c.Path == s.Path {
			info.Chunks = append(info.Chunks, c)
		}
	}
	sort.Slice(info.Chunks, func(i, j int) bool {
		if info.Chunks[i].Start != info.Chunks[j].Start {
			return info.Chunks[i].Start < info.Chunks[j].Start
		}
		return info.Chunks[i].ID < info.Chunks[j].ID
	})
	return info, nil
}

func symbolSummary(s codegraph.Symbol) codegraph.NodeSummary {
	return codegraph.NodeSummary{
		Label: codegraph.LabelSymbol, Layer: s.Layer, ID: s.ID, Kind: s.Kind,
		Name: s.Name, QualName: s.QualName, Path: s.Path, Start: s.Start,
	}
}

func fileSummary(f codegraph.File) codegraph.NodeSummary {
	return codegraph.NodeSummary{Label: codegraph.LabelFile, Layer: f.Layer, Name: f.Path, Path: f.Path}
}

func packageSummary(layer, name, kind string) codegraph.NodeSummary {
	return codegraph.NodeSummary{Label: codegraph.LabelPackage, Layer: layer, Kind: kind, Name: name}
}

// Neighbors walks visible edges from the query's start. Each visible edge is
// a row once, at the depth of the hop that first reached it; rows are
// ordered by depth, then From, then To (path, start, id, name, layer), type
// and alias.
func (g *Graph) Neighbors(ctx context.Context, view codegraph.View, q codegraph.NeighborQuery) ([]codegraph.NeighborRow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := view.Validate(); err != nil {
		return nil, err
	}
	if err := q.Validate(); err != nil {
		return nil, err
	}
	vp := viewParam(view)
	var rows []codegraph.NeighborRow
	switch q.Relation {
	case codegraph.RelImports:
		res, err := g.read(ctx, qFilesAt, params{"path": q.Path, "view": vp})
		if err != nil {
			return nil, err
		}
		f, ok := highestFile(view, res)
		if !ok {
			return nil, fmt.Errorf("%w: file %q", codegraph.ErrNotFound, q.Path)
		}
		ims, err := g.read(ctx, qImportsOf, params{"layer": f.Layer, "path": f.Path})
		if err != nil {
			return nil, err
		}
		for _, r := range ims.rows {
			rows = append(rows, codegraph.NeighborRow{
				From: fileSummary(f), Relation: q.Relation, Type: codegraph.EdgeImports,
				To: packageSummary(str(r[1]), str(r[2]), str(r[3])), Path: f.Path, Alias: str(r[0]), Depth: 1,
			})
		}
	case codegraph.RelImporters:
		res, err := g.read(ctx, qPackageInView, params{"name": q.Package, "view": vp})
		if err != nil {
			return nil, err
		}
		if len(res.rows) == 0 || num(res.rows[0][0]) == 0 {
			return nil, fmt.Errorf("%w: package %q", codegraph.ErrNotFound, q.Package)
		}
		ims, err := g.read(ctx, qImporters, params{"name": q.Package, "view": vp})
		if err != nil {
			return nil, err
		}
		for _, r := range ims.rows {
			f := parseFile(r, 0)
			at := r[nFileCols:]
			rows = append(rows, codegraph.NeighborRow{
				From: fileSummary(f), Relation: q.Relation, Type: codegraph.EdgeImports,
				To: packageSummary(str(at[1]), str(at[2]), str(at[3])), Path: f.Path, Alias: str(at[0]), Depth: 1,
			})
		}
	default:
		var err error
		if rows, err = g.walk(ctx, view, q); err != nil {
			return nil, err
		}
	}
	sort.Slice(rows, func(i, j int) bool { return lessRow(rows[i], rows[j]) })
	return limit(rows, q.Limit), nil
}

// highestFile is the file of res (rows of fileCols) in the highest layer of
// view.
func highestFile(view codegraph.View, res *result) (codegraph.File, bool) {
	var best codegraph.File
	found := false
	for _, r := range res.rows {
		f := parseFile(r, 0)
		if !found || view.Rank(f.Layer) > view.Rank(best.Layer) {
			best, found = f, true
		}
	}
	return best, found
}

// walk runs a symbol relation breadth first, one query per hop.
func (g *Graph) walk(ctx context.Context, view codegraph.View, q codegraph.NeighborQuery) ([]codegraph.NeighborRow, error) {
	vp := viewParam(view)
	res, err := g.read(ctx, qVisibleLayersOf, params{"id": q.ID, "view": vp})
	if err != nil {
		return nil, err
	}
	startRank := -1
	for _, r := range res.rows {
		startRank = max(startRank, view.Rank(str(r[0])))
	}
	if startRank < 0 {
		return nil, fmt.Errorf("%w: symbol %q", codegraph.ErrNotFound, q.ID)
	}
	start := codegraph.NodeRef{Layer: view[startRank], ID: q.ID}
	types, incoming, _ := q.Relation.Edges()
	typeNames := make([]string, len(types))
	for i, t := range types {
		typeNames[i] = string(t)
	}
	depth := q.Depth
	if depth == 0 {
		depth = 1
	}
	type edgeKey struct {
		typ      string
		layer    string
		from, to codegraph.NodeRef
	}
	rows := []codegraph.NeighborRow{}
	seenNode := map[codegraph.NodeRef]bool{start: true}
	seenEdge := map[edgeKey]bool{}
	frontier := []codegraph.NodeRef{start}
	for hop := 1; hop <= depth && len(frontier) > 0; hop++ {
		fp := make([]map[string]any, len(frontier))
		for i, n := range frontier {
			fp[i] = map[string]any{"layer": n.Layer, "id": n.ID}
		}
		res, err := g.read(ctx, qHop[incoming], params{"frontier": fp, "types": typeNames, "view": vp})
		if err != nil {
			return nil, err
		}
		var next []codegraph.NodeRef
		for _, r := range res.rows {
			from, to := parseSymbol(r, 4), parseSymbol(r, 4+nSymbolCols)
			k := edgeKey{str(r[0]), str(r[1]), ref(from), ref(to)}
			if seenEdge[k] {
				continue
			}
			seenEdge[k] = true
			rows = append(rows, codegraph.NeighborRow{
				From: symbolSummary(from), Relation: q.Relation, Type: codegraph.EdgeType(k.typ), To: symbolSummary(to),
				Path: from.Path, Line: num(r[3]), Confidence: str(r[2]), Depth: hop,
			})
			far := ref(to)
			if incoming {
				far = ref(from)
			}
			if !seenNode[far] {
				seenNode[far] = true
				next = append(next, far)
			}
		}
		frontier = next
	}
	return rows, nil
}

func cmpNode(a, b codegraph.NodeSummary) int {
	switch {
	case a.Path != b.Path:
		return strings.Compare(a.Path, b.Path)
	case a.Start != b.Start:
		if a.Start < b.Start {
			return -1
		}
		return 1
	case a.ID != b.ID:
		return strings.Compare(a.ID, b.ID)
	case a.Name != b.Name:
		return strings.Compare(a.Name, b.Name)
	}
	return strings.Compare(a.Layer, b.Layer)
}

func lessRow(a, b codegraph.NeighborRow) bool {
	if a.Depth != b.Depth {
		return a.Depth < b.Depth
	}
	if c := cmpNode(a.From, b.From); c != 0 {
		return c < 0
	}
	if c := cmpNode(a.To, b.To); c != 0 {
		return c < 0
	}
	if a.Type != b.Type {
		return a.Type < b.Type
	}
	return a.Alias < b.Alias
}

// Outline returns the symbol trees of the visible files at q.Path or in
// q.Package, ordered by path then layer rank.
func (g *Graph) Outline(ctx context.Context, view codegraph.View, q codegraph.OutlineQuery) ([]codegraph.FileOutline, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := view.Validate(); err != nil {
		return nil, err
	}
	if err := q.Validate(); err != nil {
		return nil, err
	}
	vp := viewParam(view)
	var (
		res *result
		err error
	)
	if q.Path != "" {
		res, err = g.read(ctx, qFilesAt, params{"path": q.Path, "view": vp})
	} else {
		res, err = g.read(ctx, qFilesInPackage, params{"package": q.Package, "view": vp})
	}
	if err != nil {
		return nil, err
	}
	if len(res.rows) == 0 {
		what := q.Path
		if what == "" {
			what = "package " + q.Package
		}
		return nil, fmt.Errorf("%w: %s", codegraph.ErrNotFound, what)
	}
	type fk struct{ layer, path string }
	files := make([]codegraph.File, 0, len(res.rows))
	keys := make([]map[string]any, 0, len(res.rows))
	for _, r := range res.rows {
		f := parseFile(r, 0)
		files = append(files, f)
		keys = append(keys, map[string]any{"layer": f.Layer, "path": f.Path})
	}
	sres, err := g.read(ctx, qFileSymbols, params{"files": keys})
	if err != nil {
		return nil, err
	}
	syms := map[fk][]codegraph.Symbol{}
	for _, r := range sres.rows {
		k := fk{str(r[0]), str(r[1])}
		syms[k] = append(syms[k], parseSymbol(r, 2))
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].Path != files[j].Path {
			return files[i].Path < files[j].Path
		}
		return view.Rank(files[i].Layer) < view.Rank(files[j].Layer)
	})
	out := make([]codegraph.FileOutline, 0, len(files))
	for _, f := range files {
		out = append(out, codegraph.FileOutline{File: f, Symbols: codegraph.BuildOutline(syms[fk{f.Layer, f.Path}])})
	}
	return out, nil
}

// stopwords are the words SearchText drops from a query unless they are a
// prefix term, as the memory adapter does (RediSearch's default list).
var stopwords = map[string]bool{
	"a": true, "is": true, "the": true, "an": true, "and": true, "are": true, "as": true,
	"at": true, "be": true, "but": true, "by": true, "for": true, "if": true, "in": true,
	"into": true, "it": true, "no": true, "not": true, "of": true, "on": true, "or": true,
	"such": true, "that": true, "their": true, "then": true, "there": true, "these": true,
	"they": true, "this": true, "to": true, "was": true, "will": true, "with": true,
}

// textQuery turns free text into a RediSearch query of plain terms: runs of
// letters, digits and underscores, each optionally a prefix (trailing '*'),
// all of which must match. Every other character, query syntax included, is
// a separator, so nothing in the text can reach the query parser as syntax.
// It returns "" when no term is left.
func textQuery(text string) string {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '*')
	})
	var terms []string
	for _, w := range fields {
		prefix := strings.HasSuffix(w, "*")
		w = strings.Trim(w, "*")
		if w == "" || strings.Contains(w, "*") || (!prefix && stopwords[strings.ToLower(w)]) {
			continue
		}
		if prefix {
			w += "*"
		}
		terms = append(terms, w)
	}
	return strings.Join(terms, " ")
}

// SearchText runs q.Query through the full-text index over the visible
// symbols' names, signatures and doc comments: every term must occur (a
// trailing '*' makes a prefix), case-insensitively, without stemming.
// Results are best score first, then path, start and id.
func (g *Graph) SearchText(ctx context.Context, view codegraph.View, q codegraph.TextQuery) ([]codegraph.TextHit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := view.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(q.Query) == "" {
		return nil, fmt.Errorf("%w: search needs a query", codegraph.ErrInvalid)
	}
	hits := []codegraph.TextHit{}
	query := textQuery(q.Query)
	if query == "" {
		return hits, nil
	}
	p := params{"label": codegraph.LabelSymbol, "query": query, "view": viewParam(view)}
	res, err := g.read(ctx, qSearch, p)
	if err != nil {
		return nil, err
	}
	cands, err := g.highestOnly(ctx, view, symbolsFrom(res, true))
	if err != nil {
		return nil, err
	}
	for _, c := range cands {
		if q.Filter.Match(c.s.Kind, c.lang, c.s.Path) {
			hits = append(hits, codegraph.TextHit{Symbol: c.s, Score: c.score})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return lessSymbol(hits[i].Symbol, hits[j].Symbol)
	})
	return limit(hits, q.Limit), nil
}
