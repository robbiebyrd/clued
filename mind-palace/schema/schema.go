// Package schema validates create input and stored front matter against each
// kind's JSON Schema (draft 2020-12). The embedded schemas' enumerations are
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

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/kind"
)

//go:embed plan.schema.json
var planSchemaJSON []byte

//go:embed story.schema.json
var storySchemaJSON []byte

// Raw returns the unmodified embedded schema for a kind.
func Raw(k string) []byte {
	switch k {
	case kind.Story:
		return append([]byte(nil), storySchemaJSON...)
	default:
		return append([]byte(nil), planSchemaJSON...)
	}
}

func schemaURL(k string) string { return "https://clued.dev/schemas/" + k + ".schema.json" }

// Validator holds the compiled schemas for one kind and configuration.
type Validator struct {
	kind   string
	doc    map[string]any
	create *jsonschema.Schema
	stored *jsonschema.Schema
	fm     *jsonschema.Schema
	body   *jsonschema.Schema
}

// New compiles a kind's schema with the configuration's enumerations applied.
func New(k string, cfg *config.KindConfig) (*Validator, error) {
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(Raw(k)))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode embedded %s schema: %w", k, err)
	}
	if cfg != nil {
		typeDef := "planType"
		if k == kind.Story {
			typeDef = "storyType"
		}
		patchEnum(doc, []string{"$defs", typeDef}, cfg.TypeNames())
		patchEnum(doc, []string{"$defs", "status"}, cfg.StatusNames())
		patchEnum(doc, []string{"$defs", "frontMatter", "properties", "priority"}, cfg.PriorityValues())
		patchEnum(doc, []string{"$defs", "frontMatter", "properties", "effort"}, cfg.EffortNames())
		// Keep the conditional sections for the built-in types only when
		// those types are still configured.
		filterTypeConditions(doc, cfg.TypeNames())
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	url := schemaURL(k)
	if err := c.AddResource(url, doc); err != nil {
		return nil, err
	}
	v := &Validator{kind: k, doc: doc}
	var err error
	if v.create, err = c.Compile(url); err != nil {
		return nil, fmt.Errorf("compile %s create schema: %w", k, err)
	}
	if v.stored, err = c.Compile(url + "#/$defs/storedFrontMatter"); err != nil {
		return nil, fmt.Errorf("compile %s stored front matter schema: %w", k, err)
	}
	if v.fm, err = c.Compile(url + "#/$defs/frontMatter"); err != nil {
		return nil, fmt.Errorf("compile %s front matter schema: %w", k, err)
	}
	if v.body, err = c.Compile(url + "#/$defs/body"); err != nil {
		return nil, fmt.Errorf("compile %s body schema: %w", k, err)
	}
	return v, nil
}

// Kind returns the kind this validator checks.
func (v *Validator) Kind() string { return v.kind }

// JSON returns the effective (patched) schema document.
func (v *Validator) JSON() []byte {
	b, _ := json.MarshalIndent(v.doc, "", "  ")
	return b
}

// ValidateCreate checks a create input (slug, frontMatter, body).
func (v *Validator) ValidateCreate(instance any) []string {
	return problems(v.create.Validate(toInstance(instance)))
}

// ValidateStoredFrontMatter checks the front matter of a written document.
func (v *Validator) ValidateStoredFrontMatter(instance any) []string {
	return problems(v.stored.Validate(toInstance(instance)))
}

// ValidateFrontMatter checks front matter without requiring managed fields.
func (v *Validator) ValidateFrontMatter(instance any) []string {
	return problems(v.fm.Validate(toInstance(instance)))
}

// ValidateBody checks a body.
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

var printer = message.NewPrinter(language.English)

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
