// Package webdavstore stores documents as Markdown files in a WebDAV folder
// (Nextcloud, ownCloud, Apache mod_dav, a NAS share, …). The layout mirrors
// the file store, one collection per kind under the base URL:
//
//	<url>/<plansPath>/<id>-<type>-<slug>.md            active plans
//	<url>/<plansPath>/archive/<id>-<type>-<slug>.md    archived plans
//	<url>/<plansPath>/templates/<templateId>.md        plan templates
//	<url>/<storiesPath>/…                              the same for stories
//
// Options:
//
//	url          base URL of the folder (required); a trailing slash is implied
//	plansPath    collection for plans, relative to url (default "plans")
//	storiesPath  collection for stories (default "stories"; <plural>Path for any kind)
//	username     basic-auth user; falls back to the userinfo in url, then $MIND_PALACE_WEBDAV_USERNAME
//	password     basic-auth password; falls back to the url, then $MIND_PALACE_WEBDAV_PASSWORD
//	token        bearer token instead of basic auth; falls back to $MIND_PALACE_WEBDAV_TOKEN
//	timeout      per-request timeout (Go duration, default "30s")
//
// The client speaks plain HTTP (PROPFIND, GET, PUT, DELETE, MKCOL) through
// net/http; credentials travel in headers only and never appear in errors.
package webdavstore

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/markdown"
	"github.com/robbiebyrd/clued/mind-palace/model"
	"github.com/robbiebyrd/clued/mind-palace/store"
)

// Subcollections inside each kind's collection.
const (
	ArchiveDir   = "archive"
	TemplatesDir = "templates"
)

// DefaultTimeout bounds every request when the timeout option is unset.
const DefaultTimeout = 30 * time.Second

func init() {
	f := func(def config.StorageDef, _ map[string]string) (store.Store, error) {
		return NewFromOptions(def.Name, def.Options)
	}
	store.Register("webdav", f)
	store.Register("dav", f)
}

// Options configures a store.
type Options struct {
	// URL is the folder the collections live in.
	URL string
	// Paths maps a kind name to its collection, relative to URL.
	Paths map[string]string
	// Username and Password enable basic auth; Token sends a bearer token
	// instead and wins when both are set.
	Username, Password, Token string
	Timeout                   time.Duration
	// HTTPClient overrides the transport (tests); its Timeout is left alone.
	HTTPClient *http.Client
}

// NewFromOptions builds a store from the plugin option map, applying the
// environment fallbacks described in the package comment.
func NewFromOptions(name string, opts map[string]string) (*Store, error) {
	o := Options{
		URL:      opts["url"],
		Paths:    map[string]string{},
		Username: opts["username"],
		Password: opts["password"],
		Token:    opts["token"],
	}
	for _, k := range kind.All() {
		if p := opts[k.Plural+"Path"]; p != "" {
			o.Paths[k.Name] = p
		}
	}
	if t := opts["timeout"]; t != "" {
		d, err := time.ParseDuration(t)
		if err != nil {
			return nil, fmt.Errorf("webdav %q: timeout: %w", name, err)
		}
		o.Timeout = d
	}
	if o.Username == "" {
		o.Username = os.Getenv("MIND_PALACE_WEBDAV_USERNAME")
	}
	if o.Password == "" {
		o.Password = os.Getenv("MIND_PALACE_WEBDAV_PASSWORD")
	}
	if o.Token == "" {
		o.Token = os.Getenv("MIND_PALACE_WEBDAV_TOKEN")
	}
	return New(name, o)
}

// Store is a WebDAV-backed document store.
type Store struct {
	name  string
	base  *url.URL
	roots map[string]string // kind → collection path relative to base, no slashes at the ends
	http  *http.Client
	user  string
	pass  string
	token string
}

// New creates a store. The URL's userinfo, when present, supplies the basic
// auth credentials unless Options names them.
func New(name string, o Options) (*Store, error) {
	if name == "" {
		name = "webdav"
	}
	if strings.TrimSpace(o.URL) == "" {
		return nil, fmt.Errorf("webdav %q: url option is required", name)
	}
	u, err := url.Parse(strings.TrimSpace(o.URL))
	if err != nil {
		return nil, fmt.Errorf("webdav %q: url: %w", name, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("webdav %q: url must be an http(s) URL with a host", name)
	}
	user, pass := o.Username, o.Password
	if u.User != nil {
		if user == "" {
			user = u.User.Username()
		}
		if pass == "" {
			pass, _ = u.User.Password()
		}
		u.User = nil
	}
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	u.RawQuery, u.Fragment = "", ""
	s := &Store{name: name, base: u, roots: map[string]string{}, user: user, pass: pass, token: o.Token}
	for _, k := range kind.All() {
		p := o.Paths[k.Name]
		if p == "" {
			p = k.Plural
		}
		p = strings.Trim(strings.TrimSpace(p), "/")
		if p == "" || p == "." || strings.Contains(p, "..") {
			return nil, fmt.Errorf("webdav %q: invalid %sPath %q", name, k.Plural, o.Paths[k.Name])
		}
		s.roots[k.Name] = p
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if o.HTTPClient != nil {
		s.http = o.HTTPClient
	} else {
		s.http = &http.Client{Timeout: timeout}
	}
	return s, nil
}

// Base returns the folder URL without credentials.
func (s *Store) Base() string { return s.base.String() }

func (s *Store) Name() string   { return s.name }
func (s *Store) Driver() string { return "webdav" }

// Init creates the base folder (and any missing parent below the deepest
// existing collection), one collection per kind and its archive.
func (s *Store) Init(ctx context.Context) error {
	if err := s.ensureBase(ctx); err != nil {
		return err
	}
	for _, root := range s.roots {
		for _, p := range []string{root, root + "/" + ArchiveDir} {
			if err := s.ensureCollection(ctx, p); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) Close() error {
	s.http.CloseIdleConnections()
	return nil
}

func (s *Store) root(kindName string) (string, error) {
	root, ok := s.roots[kindName]
	if !ok {
		return "", fmt.Errorf("unknown document kind %q", kindName)
	}
	return root, nil
}

// --- HTTP client -----------------------------------------------------------

// href builds the URL of a base-relative path; url.URL escapes it once.
func (s *Store) href(rel string) string {
	return s.hrefAt(s.base.Path + strings.Trim(rel, "/"))
}

// hrefAt builds the URL of an absolute path on the server.
func (s *Store) hrefAt(absPath string) string {
	u := *s.base
	u.Path, u.RawPath = absPath, ""
	return u.String()
}

// httpError is a non-success response.
type httpError struct {
	method string
	rel    string
	status int
}

func (e *httpError) Error() string {
	return fmt.Sprintf("webdav %s %q: HTTP %d %s", e.method, e.rel, e.status, http.StatusText(e.status))
}

// do sends one request and returns the response, mapping 404 to
// store.ErrNotFound and other non-2xx statuses (and 207 for PROPFIND) to an
// httpError. The caller closes the body on a nil error.
func (s *Store) do(ctx context.Context, method, rel string, body []byte, headers map[string]string, want ...int) (*http.Response, error) {
	return s.doURL(ctx, method, rel, s.href(rel), body, headers, want...)
}

// doURL is do for an explicit URL; rel only labels errors.
func (s *Store) doURL(ctx context.Context, method, rel, target string, body []byte, headers map[string]string, want ...int) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.ContentLength = int64(len(body))
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	switch {
	case s.token != "":
		req.Header.Set("Authorization", "Bearer "+s.token)
	case s.user != "" || s.pass != "":
		req.SetBasicAuth(s.user, s.pass)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		// url.Error repeats the URL, which never carries credentials here.
		return nil, fmt.Errorf("webdav %s %q: %w", method, rel, errors.Unwrap(err))
	}
	for _, w := range want {
		if resp.StatusCode == w {
			return resp, nil
		}
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, store.ErrNotFound
	}
	return nil, &httpError{method: method, rel: rel, status: resp.StatusCode}
}

// entry is one member of a collection listing.
type entry struct {
	Name     string
	Dir      bool
	Modified time.Time
}

const propfindBody = `<?xml version="1.0" encoding="utf-8"?>` +
	`<D:propfind xmlns:D="DAV:"><D:prop><D:resourcetype/><D:getlastmodified/></D:prop></D:propfind>`

// multistatus is the subset of a PROPFIND response the store reads.
type multistatus struct {
	XMLName   xml.Name   `xml:"DAV: multistatus"`
	Responses []response `xml:"DAV: response"`
}

type response struct {
	Href     string     `xml:"DAV: href"`
	Propstat []propstat `xml:"DAV: propstat"`
}

type propstat struct {
	Status string `xml:"DAV: status"`
	Prop   struct {
		ResourceType struct {
			Collection *struct{} `xml:"DAV: collection"`
		} `xml:"DAV: resourcetype"`
		LastModified string `xml:"DAV: getlastmodified"`
	} `xml:"DAV: prop"`
}

// parseMultistatus turns a PROPFIND Depth 1 body into the members of the
// collection at dirPath (the collection's own response is skipped).
func parseMultistatus(body []byte, dirPath string) ([]entry, error) {
	var ms multistatus
	if err := xml.Unmarshal(body, &ms); err != nil {
		return nil, fmt.Errorf("webdav PROPFIND: bad multistatus: %w", err)
	}
	dir := strings.TrimSuffix(dirPath, "/")
	var out []entry
	for _, r := range ms.Responses {
		p := r.Href
		if u, err := url.Parse(strings.TrimSpace(r.Href)); err == nil {
			p = u.Path
		}
		p = strings.TrimSuffix(p, "/")
		if p == dir || p == "" {
			continue
		}
		e := entry{Name: p[strings.LastIndex(p, "/")+1:]}
		for _, ps := range r.Propstat {
			if ps.Status != "" && !strings.Contains(ps.Status, " 200 ") && !strings.HasSuffix(ps.Status, " 200") {
				continue
			}
			if ps.Prop.ResourceType.Collection != nil {
				e.Dir = true
			}
			if ps.Prop.LastModified != "" {
				if t, err := http.ParseTime(ps.Prop.LastModified); err == nil {
					e.Modified = t
				}
			}
		}
		if e.Name != "" {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// list returns the members of a collection; a missing collection is
// reported as store.ErrNotFound.
func (s *Store) list(ctx context.Context, rel string) ([]entry, error) {
	resp, err := s.do(ctx, "PROPFIND", rel+"/", []byte(propfindBody), map[string]string{
		"Depth":        "1",
		"Content-Type": "application/xml; charset=utf-8",
	}, http.StatusMultiStatus)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	u, _ := url.Parse(s.href(rel))
	return parseMultistatus(body, u.Path)
}

// stat returns the entry of one resource (Depth 0).
func (s *Store) stat(ctx context.Context, rel string) (entry, error) {
	resp, err := s.do(ctx, "PROPFIND", rel, []byte(propfindBody), map[string]string{
		"Depth":        "0",
		"Content-Type": "application/xml; charset=utf-8",
	}, http.StatusMultiStatus)
	if err != nil {
		return entry{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return entry{}, err
	}
	// Parsing against the parent keeps the resource itself in the result.
	parent := rel[:strings.LastIndex(rel, "/")+1]
	u, _ := url.Parse(s.href(parent))
	entries, err := parseMultistatus(body, u.Path)
	if err != nil {
		return entry{}, err
	}
	if len(entries) == 0 {
		return entry{}, store.ErrNotFound
	}
	return entries[0], nil
}

func (s *Store) get(ctx context.Context, rel string) ([]byte, error) {
	resp, err := s.do(ctx, http.MethodGet, rel, nil, nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (s *Store) put(ctx context.Context, rel string, data []byte) error {
	resp, err := s.do(ctx, http.MethodPut, rel, data, map[string]string{"Content-Type": "text/markdown; charset=utf-8"},
		http.StatusOK, http.StatusCreated, http.StatusNoContent)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	return resp.Body.Close()
}

func (s *Store) delete(ctx context.Context, rel string) error {
	resp, err := s.do(ctx, http.MethodDelete, rel, nil, nil, http.StatusOK, http.StatusNoContent)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	return resp.Body.Close()
}

// ensureCollection creates rel (and its parents under the base) with MKCOL.
// A collection that already exists is left alone.
func (s *Store) ensureCollection(ctx context.Context, rel string) error {
	rel = strings.Trim(rel, "/")
	if rel == "" {
		return s.mkcol(ctx, "")
	}
	segs := strings.Split(rel, "/")
	for i := range segs {
		if err := s.mkcol(ctx, strings.Join(segs[:i+1], "/")); err != nil {
			return err
		}
	}
	return nil
}

// mkcol creates one collection under the base. 405 Method Not Allowed is
// how servers answer MKCOL on an existing collection, so it counts as done.
func (s *Store) mkcol(ctx context.Context, rel string) error {
	target := rel + "/"
	if rel == "" {
		target = ""
	}
	return s.mkcolURL(ctx, target, s.href(target))
}

func (s *Store) mkcolURL(ctx context.Context, label, target string) error {
	resp, err := s.doURL(ctx, "MKCOL", label, target, nil, nil, http.StatusCreated, http.StatusOK, http.StatusMethodNotAllowed)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	return resp.Body.Close()
}

// ensureBase makes the base folder exist. When MKCOL on it fails with 409
// (a parent is missing) the deepest existing ancestor is found with PROPFIND
// and the folders below it are created one by one. Servers that refuse
// MKCOL on an existing root share (403) are accepted when the base lists.
func (s *Store) ensureBase(ctx context.Context) error {
	err := s.mkcol(ctx, "")
	var he *httpError
	if err == nil || !errors.As(err, &he) {
		return err
	}
	switch he.status {
	case http.StatusForbidden:
		if _, lerr := s.list(ctx, ""); lerr == nil {
			return nil
		}
		return err
	case http.StatusConflict:
	default:
		return err
	}
	segs := strings.Split(strings.Trim(s.base.Path, "/"), "/")
	// Walk up until a collection answers PROPFIND, then create downwards.
	exists := 0
	for i := len(segs) - 1; i >= 1; i-- {
		p := "/" + strings.Join(segs[:i], "/") + "/"
		resp, perr := s.doURL(ctx, "PROPFIND", p, s.hrefAt(p), []byte(propfindBody), map[string]string{"Depth": "0", "Content-Type": "application/xml; charset=utf-8"}, http.StatusMultiStatus)
		if perr == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			exists = i
			break
		}
	}
	if exists == 0 {
		return err
	}
	for i := exists + 1; i <= len(segs); i++ {
		p := "/" + strings.Join(segs[:i], "/") + "/"
		if merr := s.mkcolURL(ctx, p, s.hrefAt(p)); merr != nil {
			return merr
		}
	}
	return nil
}

// --- documents --------------------------------------------------------------

// scan lists document files in the root and archive collections of a kind.
func (s *Store) scan(ctx context.Context, kindName string) ([]string, error) {
	root, err := s.root(kindName)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, sub := range []string{"", ArchiveDir} {
		rel := root
		if sub != "" {
			rel += "/" + sub
		}
		entries, err := s.list(ctx, rel)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			if e.Dir {
				continue
			}
			if _, _, _, ok := model.ParseFileName(e.Name); !ok {
				continue
			}
			if sub != "" {
				out = append(out, sub+"/"+e.Name)
			} else {
				out = append(out, e.Name)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

func (s *Store) read(ctx context.Context, kindName, rel string) (*model.Document, error) {
	root, err := s.root(kindName)
	if err != nil {
		return nil, err
	}
	data, err := s.get(ctx, root+"/"+rel)
	if err != nil {
		return nil, err
	}
	fm, content, err := markdown.Parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	if fm.ID == "" {
		if id, _, _, ok := model.ParseFileName(rel); ok {
			fm.ID = id
		}
	}
	return &model.Document{Kind: kindName, Path: rel, FrontMatter: fm, Content: content}, nil
}

func (s *Store) List(ctx context.Context, kindName string) ([]*model.Document, error) {
	paths, err := s.scan(ctx, kindName)
	if err != nil {
		return nil, err
	}
	var out []*model.Document
	for _, rel := range paths {
		d, err := s.read(ctx, kindName, rel)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out, nil
}

// findPath locates the file holding a document ID.
func (s *Store) findPath(ctx context.Context, kindName, id string) (string, error) {
	paths, err := s.scan(ctx, kindName)
	if err != nil {
		return "", err
	}
	for _, rel := range paths {
		if fid, _, _, _ := model.ParseFileName(rel); fid == id {
			return rel, nil
		}
	}
	return "", store.ErrNotFound
}

func (s *Store) Get(ctx context.Context, kindName, id string) (*model.Document, error) {
	rel, err := s.findPath(ctx, kindName, id)
	if err != nil {
		return nil, err
	}
	return s.read(ctx, kindName, rel)
}

// checkPath accepts a document path of the file-store layout: a file name,
// optionally under archive/.
func checkPath(rel string) error {
	if rel == "" {
		return errors.New("document has no path")
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.ContainsAny(seg, `\`) {
			return fmt.Errorf("invalid document path %q", rel)
		}
	}
	return nil
}

func (s *Store) Put(ctx context.Context, d *model.Document) error {
	if err := checkPath(d.Path); err != nil {
		return err
	}
	root, err := s.root(d.Kind)
	if err != nil {
		return err
	}
	doc, err := markdown.RenderDocument(d)
	if err != nil {
		return err
	}
	prev, err := s.findPath(ctx, d.Kind, d.ID())
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if i := strings.LastIndex(d.Path, "/"); i >= 0 {
		if err := s.ensureCollection(ctx, root+"/"+d.Path[:i]); err != nil {
			return err
		}
	}
	if err := s.put(ctx, root+"/"+d.Path, []byte(doc)); err != nil {
		return err
	}
	if prev != "" && prev != d.Path {
		if err := s.delete(ctx, root+"/"+prev); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, kindName, id string) error {
	rel, err := s.findPath(ctx, kindName, id)
	if err != nil {
		return err
	}
	root, _ := s.root(kindName)
	return s.delete(ctx, root+"/"+rel)
}

// --- templates --------------------------------------------------------------

func checkTemplateID(id string) error {
	if id == "" || strings.ContainsAny(id, `/\`) || id == "." || id == ".." {
		return fmt.Errorf("invalid template id %q", id)
	}
	return nil
}

func (s *Store) templatePath(root, id string) string {
	return root + "/" + TemplatesDir + "/" + id + ".md"
}

func (s *Store) ListTemplates(ctx context.Context, kindName string) ([]*model.Template, error) {
	root, err := s.root(kindName)
	if err != nil {
		return nil, err
	}
	entries, err := s.list(ctx, root+"/"+TemplatesDir)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	var out []*model.Template
	for _, e := range entries {
		if e.Dir || !strings.HasSuffix(e.Name, ".md") {
			continue
		}
		id := strings.TrimSuffix(e.Name, ".md")
		data, err := s.get(ctx, s.templatePath(root, id))
		if err != nil {
			return nil, err
		}
		out = append(out, &model.Template{Kind: kindName, ID: id, Content: string(data), Updated: updatedOf(e)})
	}
	return out, nil
}

func (s *Store) GetTemplate(ctx context.Context, kindName, id string) (*model.Template, error) {
	if err := checkTemplateID(id); err != nil {
		return nil, err
	}
	root, err := s.root(kindName)
	if err != nil {
		return nil, err
	}
	data, err := s.get(ctx, s.templatePath(root, id))
	if err != nil {
		return nil, err
	}
	t := &model.Template{Kind: kindName, ID: id, Content: string(data)}
	if e, err := s.stat(ctx, s.templatePath(root, id)); err == nil {
		t.Updated = updatedOf(e)
	} else {
		t.Updated = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return t, nil
}

// updatedOf renders an entry's modification time; servers without
// getlastmodified get the current time, so Updated is never empty.
func updatedOf(e entry) string {
	if e.Modified.IsZero() {
		return time.Now().UTC().Format(time.RFC3339Nano)
	}
	return e.Modified.UTC().Format(time.RFC3339Nano)
}

func (s *Store) PutTemplate(ctx context.Context, t *model.Template) error {
	if err := checkTemplateID(t.ID); err != nil {
		return err
	}
	root, err := s.root(t.Kind)
	if err != nil {
		return err
	}
	if err := s.ensureCollection(ctx, root+"/"+TemplatesDir); err != nil {
		return err
	}
	return s.put(ctx, s.templatePath(root, t.ID), []byte(t.Content))
}

func (s *Store) DeleteTemplate(ctx context.Context, kindName, id string) error {
	if err := checkTemplateID(id); err != nil {
		return err
	}
	root, err := s.root(kindName)
	if err != nil {
		return err
	}
	return s.delete(ctx, s.templatePath(root, id))
}

var _ store.Store = (*Store)(nil)
