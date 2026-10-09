// Package ops is the operation registry shared by every entrypoint. Each
// operation has a name, a typed parameter struct (from which a JSON Schema is
// derived for MCP tools and documentation) and a handler. The CLI, HTTP, WebSocket
// and MCP entrypoints all dispatch through the same registry, so they accept
// the same function names, inputs and return the same data.
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

	"github.com/robbiebyrd/clued/plan/service"
)

// Op describes one registered operation.
type Op struct {
	Name        string
	Description string
	// ReadOnly operations never write to storage.
	ReadOnly bool
	// Group is a documentation grouping (plans, templates, fields, links, progress, storage).
	Group string

	paramsType reflect.Type
	invoke     func(ctx context.Context, svc *service.Service, params any) (any, error)
	schemaFn   func(svc *service.Service) *jsonschema.Schema

	schemaOnce sync.Once
	schema     *jsonschema.Schema
	schemaErr  error
}

// InputSchema returns the JSON Schema for the operation's parameters.
func (o *Op) InputSchema(svc *service.Service) (*jsonschema.Schema, error) {
	if o.schemaFn != nil {
		return o.schemaFn(svc), nil
	}
	o.schemaOnce.Do(func() {
		o.schema, o.schemaErr = jsonschema.ForType(o.paramsType, &jsonschema.ForOptions{IgnoreInvalidTypes: true})
	})
	return o.schema, o.schemaErr
}

// NewParams returns a zero value of the parameter struct.
func (o *Op) NewParams() any { return reflect.New(o.paramsType).Interface() }

// Result is what an invocation returns.
type Result struct {
	// Value is the operation result (a plan, a report, a list…).
	Value any `json:"result"`
	// Warning is set when the write succeeded on the primary store but a
	// secondary copy failed (see service.Error.Partial).
	Warning *service.Error `json:"warning,omitempty"`
}

// Registry holds operations by name.
type Registry struct {
	mu   sync.RWMutex
	ops  map[string]*Op
	list []*Op
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{ops: map[string]*Op{}} }

// Register adds a typed operation to the registry.
func Register[P any](r *Registry, op *Op, fn func(ctx context.Context, svc *service.Service, p P) (any, error)) *Op {
	var zero P
	op.paramsType = reflect.TypeOf(zero)
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

// Invoke decodes raw JSON parameters and runs the operation.
func (r *Registry) Invoke(ctx context.Context, svc *service.Service, name string, raw json.RawMessage) (*Result, error) {
	op, ok := r.Get(name)
	if !ok {
		return nil, &service.Error{Kind: service.KindBadRequest, Message: fmt.Sprintf("unknown operation %q (known: %s)", name, strings.Join(r.Names(), ", "))}
	}
	params := op.NewParams()
	if len(bytes.TrimSpace(raw)) > 0 && string(bytes.TrimSpace(raw)) != "null" {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(params); err != nil {
			return nil, &service.Error{Kind: service.KindBadRequest, Message: fmt.Sprintf("%s: invalid parameters: %v", op.Name, err)}
		}
	}
	return op.Call(ctx, svc, params)
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
	Name        string `json:"name"`
	Group       string `json:"group"`
	Description string `json:"description"`
	ReadOnly    bool   `json:"readOnly"`
	Params      any    `json:"params"`
}

// Describe returns documentation for every operation.
func (r *Registry) Describe(svc *service.Service) []Describe {
	var out []Describe
	for _, op := range r.List() {
		s, _ := op.InputSchema(svc)
		out = append(out, Describe{Name: op.Name, Group: op.Group, Description: op.Description, ReadOnly: op.ReadOnly, Params: s})
	}
	return out
}
