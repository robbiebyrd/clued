// Package schema validates plan input and stored front matter against the
// Plan JSON Schema (draft 2020-12). The embedded schema's enumerations are
// replaced at load time with the configured types, statuses, priorities and
// efforts so configuration and validation never disagree.
package schema

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/robbiebyrd/clued/plan/config"
)

//go:embed plan.schema.json
var planSchemaJSON []byte

// SchemaURL is the resource name the compiler knows the schema by.
const SchemaURL = "https://clued.dev/schemas/plan.schema.json"

// Validator holds compiled schemas for one configuration.
type Validator struct {
	doc         map[string]any
	create      *jsonschema.Schema
	stored      *jsonschema.Schema
	frontMatter *jsonschema.Schema
	body        *jsonschema.Schema
}

// RawSchema returns the unmodified embedded schema.
func RawSchema() []byte { return append([]byte(nil), planSchemaJSON...) }

// New compiles the schema with the configuration's enumerations applied.
func New(cfg *config.Config) (*Validator, error) {
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(planSchemaJSON))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode embedded schema: %w", err)
	}
	if cfg != nil {
		patchEnum(doc, []string{"$defs", "planType"}, cfg.TypeNames())
		patchEnum(doc, []string{"$defs", "status"}, cfg.StatusNames())
		patchEnum(doc, []string{"$defs", "frontMatter", "properties", "priority"}, cfg.PriorityValues())
		patchEnum(doc, []string{"$defs", "frontMatter", "properties", "effort"}, cfg.EffortNames())
		// Keep the conditional sections for the built-in types only when those
		// types are still configured; a custom type set drops the conditions
		// that no longer apply.
		filterTypeConditions(doc, cfg.TypeNames())
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	if err := c.AddResource(SchemaURL, doc); err != nil {
		return nil, err
	}
	v := &Validator{doc: doc}
	var err error
	if v.create, err = c.Compile(SchemaURL); err != nil {
		return nil, fmt.Errorf("compile create schema: %w", err)
	}
	if v.stored, err = c.Compile(SchemaURL + "#/$defs/storedFrontMatter"); err != nil {
		return nil, fmt.Errorf("compile stored front matter schema: %w", err)
	}
	if v.frontMatter, err = c.Compile(SchemaURL + "#/$defs/frontMatter"); err != nil {
		return nil, fmt.Errorf("compile front matter schema: %w", err)
	}
	if v.body, err = c.Compile(SchemaURL + "#/$defs/body"); err != nil {
		return nil, fmt.Errorf("compile body schema: %w", err)
	}
	return v, nil
}

// JSON returns the effective (patched) schema document.
func (v *Validator) JSON() []byte {
	b, _ := json.MarshalIndent(v.doc, "", "  ")
	return b
}

// ValidateCreate checks a create input (slug, frontMatter, body).
func (v *Validator) ValidateCreate(instance any) []string {
	return problems(v.create.Validate(toInstance(instance)))
}

// ValidateStoredFrontMatter checks the front matter of a written plan.
func (v *Validator) ValidateStoredFrontMatter(instance any) []string {
	return problems(v.stored.Validate(toInstance(instance)))
}

// ValidateFrontMatter checks front matter without requiring managed fields.
func (v *Validator) ValidateFrontMatter(instance any) []string {
	return problems(v.frontMatter.Validate(toInstance(instance)))
}

// ValidateBody checks a plan body.
func (v *Validator) ValidateBody(instance any) []string {
	return problems(v.body.Validate(toInstance(instance)))
}

// toInstance converts any Go value into the generic JSON form the validator
// expects (maps, slices, json.Number, strings, bools, nil).
func toInstance(x any) any {
	b, err := json.Marshal(x)
	if err != nil {
		return x
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return x
	}
	return inst
}

// problems flattens a validation error into "location: message" strings by
// walking the error tree: every leaf failure is reported with the JSON
// pointer of the offending value.
func problems(err error) []string {
	if err == nil {
		return nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return []string{err.Error()}
	}
	seen := map[string]bool{}
	var list []string
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			loc := "/" + strings.Join(e.InstanceLocation, "/")
			msg := loc + ": " + e.ErrorKind.LocalizedString(printer)
			if !seen[msg] {
				seen[msg] = true
				list = append(list, msg)
			}
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	if len(list) == 0 {
		list = append(list, ve.Error())
	}
	sort.Strings(list)
	return list
}

var printer = message.NewPrinter(language.English)

func patchEnum(doc map[string]any, path []string, values []string) {
	node := any(doc)
	for _, key := range path {
		m, ok := node.(map[string]any)
		if !ok {
			return
		}
		node = m[key]
	}
	m, ok := node.(map[string]any)
	if !ok {
		return
	}
	enum := make([]any, len(values))
	for i, v := range values {
		enum[i] = v
	}
	m["enum"] = enum
}

func filterTypeConditions(doc map[string]any, types []string) {
	all, ok := doc["allOf"].([]any)
	if !ok {
		return
	}
	known := map[string]bool{}
	for _, t := range types {
		known[t] = true
	}
	var kept []any
	for _, cond := range all {
		m, _ := cond.(map[string]any)
		typ := conditionType(m)
		if typ == "" || known[typ] {
			kept = append(kept, cond)
		}
	}
	doc["allOf"] = kept
}

func conditionType(cond map[string]any) string {
	ifm, _ := cond["if"].(map[string]any)
	props, _ := ifm["properties"].(map[string]any)
	fm, _ := props["frontMatter"].(map[string]any)
	fmProps, _ := fm["properties"].(map[string]any)
	t, _ := fmProps["type"].(map[string]any)
	s, _ := t["const"].(string)
	return s
}
