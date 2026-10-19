package resolvers

import (
	"context"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// The SQL family. A database is one catalog: files scope nothing, schemas
// do. A definition's schema is the one its qualified name spells
// (CREATE TABLE billing.users is billing.users, and nests under CREATE
// SCHEMA billing when the file has it; a trigger or an index written
// on billing.users is billing.users.t) or, for a name written without
// one, its file's default schema: the database USE names (MySQL, T-SQL;
// the extractor's package), else public. A file's package key is its
// default schema, so a definition written without a schema lives in the
// schema its file is keyed by. Names compare without case; the extractor
// strips quotes, so a quoted identifier compares without case too.
//
// A bare reference (FROM invoices, CALL send_reminders()) resolves in the
// file's default schema: the file's own definitions, then the other
// files'; otherwise, at confidence name, to a schema object of that name
// in any schema, since the search path a connection sets is not in the
// code. A schema-qualified reference (billing.users, public.invoices,
// billing.users.email) resolves by the qualified name (confidence
// qualified); when the schema it names holds a definition written without
// a schema (CREATE TABLE invoices in a file keyed public), the qualifier is
// bound to that schema's package so the definition is found exactly. Any
// other qualifier (a table alias: u.email) reaches a column of that name,
// nearest first, at confidence name.
//
// A common table expression is no definition: a reference to one (WITH
// recent AS (...) SELECT ... FROM recent) finds no schema object and
// stays unresolved unless a table of that name exists.
func init() { Register(codeparse.FamilySQL, sqlFamily{}) }

type sqlFamily struct{}

var sqlRules = Rules{Separators: []string{"."}, CaseInsensitive: true, NameFallback: true, MemberFallback: true}

// SQLDefaultSchema is the schema of a file that names no database.
const SQLDefaultSchema = "public"

// Symbol kinds of the SQL extractor the resolver tells apart.
const (
	sqlKindSchema  = "schema"
	sqlKindColumn  = "column"
	sqlKindTrigger = "trigger"
	sqlKindIndex   = "index"
)

// Rules implements Resolver.
func (sqlFamily) Rules() Rules { return sqlRules }

// Package implements Resolver: SQLFileSchema.
func (sqlFamily) Package(_ context.Context, _ View, f *File) (string, error) {
	return SQLFileSchema(f.Facts), nil
}

// SQLFileSchema is the schema a file's definitions written without one
// live in and its bare references are looked up in: the database its USE
// names (the declared package), else SQLDefaultSchema; normalized by
// SQLIdent.
func SQLFileSchema(facts *codeparse.Facts) string {
	if facts != nil {
		if s := SQLIdent(facts.Package); s != "" {
			return s
		}
	}
	return SQLDefaultSchema
}

// SQLIdent normalizes an identifier as the database compares it: a
// quoted one ("Users", `Users`, [Users]) keeps its case without the
// quotes, any other is lower-cased.
func SQLIdent(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		switch first, last := s[0], s[len(s)-1]; {
		case first == '"' && last == '"':
			return strings.ReplaceAll(s[1:len(s)-1], `""`, `"`)
		case first == '`' && last == '`':
			return strings.ReplaceAll(s[1:len(s)-1], "``", "`")
		case first == '[' && last == ']':
			return s[1 : len(s)-1]
		}
	}
	return strings.ToLower(s)
}

// sqlObject reports whether c is a schema object: a definition at top
// level or directly under a CREATE SCHEMA of its file, not a column.
func sqlObject(c Candidate) bool {
	if c.Symbol.Kind == sqlKindColumn || c.Symbol.Kind == sqlKindSchema {
		return false
	}
	return c.TopLevel() || sqlUnderSchema(c)
}

// sqlUnderSchema reports whether c's parent is a CREATE SCHEMA.
func sqlUnderSchema(c Candidate) bool {
	if c.TopLevel() || c.File.Facts == nil {
		return false
	}
	for i := range c.File.Facts.Symbols {
		if s := &c.File.Facts.Symbols[i]; s.ID == c.Symbol.Parent {
			return s.Kind == sqlKindSchema
		}
	}
	return false
}

// sqlSchemaOf is the schema of a schema object: the one its qualified
// name spells, else its file's package; "" for anything else (a column).
func sqlSchemaOf(c Candidate) string {
	if !sqlObject(c) {
		return ""
	}
	if s, ok := sqlSpelledSchema(c.Symbol); ok {
		return s
	}
	return c.File.Package
}

// sqlSpelledSchema is the schema a definition's qualified name spells:
// billing in billing.users, and in billing.users.users_touch for a
// trigger or an index (named after their table); false when it spells
// none.
func sqlSpelledSchema(sym *codeparse.Symbol) (string, bool) {
	parts := strings.Split(sym.QualName, ".")
	at := len(parts) - 2 // the part before the name
	if sym.Kind == sqlKindTrigger || sym.Kind == sqlKindIndex || sym.Kind == sqlKindColumn {
		if len(parts) < 3 {
			return "", false // on a table written without a schema
		}
		at = len(parts) - 3
	}
	if at < 0 {
		return "", false
	}
	s := SQLIdent(parts[at])
	return s, s != ""
}

// Scope implements Resolver: a qualifier that names a schema holding a
// definition written without a schema, of the name it qualifies, is bound
// to that schema's package. Other qualifiers stay free, so the engine
// matches them against qualified names.
func (sqlFamily) Scope(_ context.Context, v View, f *File) (Scope, error) {
	var s Scope
	if f.Facts == nil {
		return s, nil
	}
	for _, ref := range f.Facts.Refs {
		if ref.Qualifier == "" {
			continue
		}
		parts := strings.Split(ref.Qualifier, ".")
		var name string
		switch len(parts) {
		case 1: // schema.name
			name = ref.Name
		case 2: // schema.table.column
			name = parts[1]
		default:
			continue
		}
		if schema := SQLIdent(parts[0]); sqlHoldsUnqualified(v, schema, SQLIdent(name)) {
			s.AddQualifier(parts[0], schema)
		}
	}
	return s, nil
}

// sqlHoldsUnqualified reports whether a file keyed schema defines name at
// top level without spelling a schema.
func sqlHoldsUnqualified(v View, schema, name string) bool {
	for _, f := range v.Package(codeparse.FamilySQL, schema) {
		if f.Facts == nil {
			continue
		}
		for i := range f.Facts.Symbols {
			sym := &f.Facts.Symbols[i]
			if sym.Parent != "" || sym.Kind == sqlKindColumn || SQLIdent(sym.Name) != name {
				continue
			}
			if _, spelled := sqlSpelledSchema(sym); !spelled {
				return true
			}
		}
	}
	return false
}

// Visible implements Resolver: a bare name reaches the enclosing
// definitions' members but not a column (CREATE TABLE "User" ("role"
// "Role"): the type, not the column itself), and, through the file and
// package steps, the objects of the file's default schema; a bound schema
// qualifier the objects that live in the schema of their file's key; the
// name fallback any schema object; a member reached through a free
// qualifier only a column or another member of a table, never an object
// of a schema.
func (sqlFamily) Visible(f *File, c Candidate, step Step) bool {
	switch step {
	case StepScope:
		return c.Symbol.Kind != sqlKindColumn
	case StepFile, StepPackage:
		return sqlSchemaOf(c) == SQLFileSchema(f.Facts)
	case StepImport, StepOpen:
		return sqlSchemaOf(c) == c.File.Package
	case StepName:
		return sqlObject(c)
	case StepMember:
		return !sqlUnderSchema(c)
	}
	return true
}
