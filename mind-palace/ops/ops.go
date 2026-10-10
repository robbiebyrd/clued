// Package ops is the operation registry shared by every entrypoint. Each
// operation has a name, a typed parameter struct (from which a JSON Schema is
// derived for MCP tools and documentation) and a handler. The CLI, HTTP,
// WebSocket and MCP entrypoints all dispatch through the same registry, so
// they accept the same function names and inputs and return the same data.
//
// A registry is built for one document kind (plans or stories): the same
// operation names exist for both, and the document identifier parameter is
// named after the kind ("plan" or "story"; "document" is always accepted).
package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/service"
)

// Op describes one registered operation.
type Op struct {
	Name        string
	Description string
	// ReadOnly operations never write to storage.
	ReadOnly bool
	// Group is a documentation grouping (documents, templates, fields, links,
	// progress, criteria, storage).
	Group string
	// Destructive marks operations that delete data (MCP annotation).
	Destructive bool

	reg        *Registry
	paramsType reflect.Type
	fields     map[string]bool
	invoke     func(ctx context.Context, svc *service.Service, params any) (any, error)
	schemaFn   func(svc *service.Service) *jsonschema.Schema

	schemaOnce sync.Once
	schema     *jsonschema.Schema
	schemaErr  error
}

// InputSchema returns the JSON Schema for the operation's parameters, with
// the kind's parameter names applied.
func (o *Op) InputSchema(svc *service.Service) (*jsonschema.Schema, error) {
	if o.schemaFn != nil {
		return o.schemaFn(svc), nil
	}
	o.schemaOnce.Do(func() {
		o.schema, o.schemaErr = jsonschema.ForType(o.paramsType, &jsonschema.ForOptions{IgnoreInvalidTypes: true})
		if o.schemaErr == nil {
			o.schema = o.reg.publicSchema(o.schema)
		}
	})
	return o.schema, o.schemaErr
}

// NewParams returns a zero value of the parameter struct.
func (o *Op) NewParams() any { return reflect.New(o.paramsType).Interface() }

// Result is what an invocation returns.
type Result struct {
	// Value is the operation result (a document, a report, a list…).
	Value any `json:"result"`
	// Warning is set when the write succeeded on the primary store but a
	// secondary copy failed (see service.Error.Partial).
	Warning *service.Error `json:"warning,omitempty"`
}

// Registry holds the operations of one kind by name.
type Registry struct {
	kind *kind.Kind
	mu   sync.RWMutex
	ops  map[string]*Op
	list []*Op
	// aliases maps a public parameter name to the struct field's JSON name
	// ("plan" → "document", "step" → "section").
	aliases map[string]string
}

// NewRegistry returns an empty registry for a kind.
func NewRegistry(k *kind.Kind) *Registry {
	r := &Registry{kind: k, ops: map[string]*Op{}, aliases: map[string]string{}}
	if k != nil {
		r.aliases[k.Name] = "document"
		if k.ProgressKeyNoun != "section" {
			r.aliases[k.ProgressKeyNoun] = "section"
		}
	}
	return r
}

// Kind returns the registry's kind.
func (r *Registry) Kind() *kind.Kind { return r.kind }

// Register adds a typed operation to the registry.
func Register[P any](r *Registry, op *Op, fn func(ctx context.Context, svc *service.Service, p P) (any, error)) *Op {
	var zero P
	op.reg = r
	op.paramsType = reflect.TypeOf(zero)
	op.fields = jsonFields(op.paramsType)
	op.invoke = func(ctx context.Context, svc *service.Service, params any) (any, error) {
		p, ok := params.(*P)
		if !ok {
			return nil, fmt.Errorf("internal: wrong params type for %s", op.Name)
		}
		return fn(ctx, svc, *p)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.ops[op.Name]; dup {
		panic("duplicate operation " + op.Name)
	}
	r.ops[op.Name] = op
	r.list = append(r.list, op)
	return op
}

// jsonFields lists the JSON property names of a struct type.
func jsonFields(t reflect.Type) map[string]bool {
	out := map[string]bool{}
	if t == nil || t.Kind() != reflect.Struct {
		return out
	}
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "" {
			name = t.Field(i).Name
		}
		if name != "-" {
			out[name] = true
		}
	}
	return out
}

// Get looks an operation up by name (case-insensitive, hyphens/underscores ignored).
func (r *Registry) Get(name string) (*Op, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if op, ok := r.ops[name]; ok {
		return op, true
	}
	n := canon(name)
	for k, op := range r.ops {
		if canon(k) == n {
			return op, true
		}
	}
	return nil, false
}

func canon(s string) string {
	return strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.ToLower(s))
}

// List returns the operations in registration order.
func (r *Registry) List() []*Op {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]*Op(nil), r.list...)
}

// Names returns the operation names, sorted.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.ops))
	for n := range r.ops {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Invoke decodes raw JSON parameters and runs the operation. Public
// parameter names (the kind's identifier name, its progress noun) are mapped
// to the struct fields first.
func (r *Registry) Invoke(ctx context.Context, svc *service.Service, name string, raw json.RawMessage) (*Result, error) {
	op, ok := r.Get(name)
	if !ok {
		return nil, &service.Error{Kind: service.KindBadRequest, Message: fmt.Sprintf("unknown operation %q (known: %s)", name, strings.Join(r.Names(), ", "))}
	}
	if svc != nil && r.kind != nil && svc.Kind() != r.kind {
		return nil, &service.Error{Kind: service.KindBadRequest, Message: fmt.Sprintf("operation %s belongs to the %s registry, not %s", op.Name, r.kind.Name, svc.Kind().Name)}
	}
	params := op.NewParams()
	if len(bytes.TrimSpace(raw)) > 0 && string(bytes.TrimSpace(raw)) != "null" {
		raw, err := op.internalJSON(raw)
		if err != nil {
			return nil, &service.Error{Kind: service.KindBadRequest, Message: fmt.Sprintf("%s: invalid parameters: %v", op.Name, err)}
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(params); err != nil {
			return nil, &service.Error{Kind: service.KindBadRequest, Message: fmt.Sprintf("%s: invalid parameters: %v", op.Name, err)}
		}
	}
	return op.Call(ctx, svc, params)
}

// internalJSON renames public parameter keys to their struct field names.
func (o *Op) internalJSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(o.reg.aliases) == 0 {
		return raw, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parameters must be a JSON object: %w", err)
	}
	changed := false
	for public, internal := range o.reg.aliases {
		v, ok := m[public]
		if !ok || !o.fields[internal] || o.fields[public] {
			continue
		}
		if _, dup := m[internal]; dup {
			return nil, fmt.Errorf("both %q and %q were given", public, internal)
		}
		delete(m, public)
		m[internal] = v
		changed = true
	}
	if !changed {
		return raw, nil
	}
	return json.Marshal(m)
}

// publicSchema renames struct field properties to the kind's public names.
func (r *Registry) publicSchema(s *jsonschema.Schema) *jsonschema.Schema {
	if s == nil || len(r.aliases) == 0 {
		return s
	}
	c := *s
	c.Properties = map[string]*jsonschema.Schema{}
	for k, v := range s.Properties {
		c.Properties[k] = v
	}
	c.Required = append([]string(nil), s.Required...)
	for public, internal := range r.aliases {
		prop, ok := c.Properties[internal]
		if !ok || c.Properties[public] != nil {
			continue
		}
		delete(c.Properties, internal)
		c.Properties[public] = prop
		for i, req := range c.Required {
			if req == internal {
				c.Required[i] = public
			}
		}
	}
	return &c
}

// Call runs the operation with already-decoded parameters.
func (o *Op) Call(ctx context.Context, svc *service.Service, params any) (*Result, error) {
	v, err := o.invoke(ctx, svc, params)
	if err != nil {
		e := service.AsError(err)
		if e.Partial && v != nil {
			return &Result{Value: v, Warning: e}, nil
		}
		return nil, e
	}
	return &Result{Value: v}, nil
}

// Describe is a serialisable description of an operation.
type Describe struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Group       string `json:"group"`
	Description string `json:"description"`
	ReadOnly    bool   `json:"readOnly"`
	Params      any    `json:"params"`
}

// Describe returns documentation for every operation.
func (r *Registry) Describe(svc *service.Service) []Describe {
	var out []Describe
	kindName := ""
	if r.kind != nil {
		kindName = r.kind.Name
	}
	for _, op := range r.List() {
		s, _ := op.InputSchema(svc)
		out = append(out, Describe{Kind: kindName, Name: op.Name, Group: op.Group, Description: op.Description, ReadOnly: op.ReadOnly, Params: s})
	}
	return out
}

// Registries holds one registry per kind.
type Registries map[string]*Registry

// All builds the registries of every kind.
func All() Registries {
	out := Registries{}
	for _, k := range kind.All() {
		out[k.Name] = For(k)
	}
	return out
}

// Get returns the registry of a kind name or plural.
func (rs Registries) Get(kindName string) (*Registry, error) {
	k, ok := kind.Get(kindName)
	if !ok {
		return nil, &service.Error{Kind: service.KindBadRequest, Message: fmt.Sprintf("unknown document kind %q (known: %s)", kindName, strings.Join(kind.Names(), ", "))}
	}
	return rs[k.Name], nil
}

// Default returns the plan registry.
func Default() *Registry { return For(kind.Must(kind.Plan)) }
