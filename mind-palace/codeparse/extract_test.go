package codeparse

import "testing"

// symbolsByQual parses src as file and indexes its symbols by qualified name.
func symbolsByQual(t *testing.T, file, src string) map[string]Symbol {
	t.Helper()
	facts, err := engineForTest(t).Parse(file, []byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out := map[string]Symbol{}
	for _, s := range facts.Symbols {
		out[s.QualName] = s
	}
	return out
}

func TestNestedReceiverQualifiesAndAttaches(t *testing.T) {
	syms := symbolsByQual(t, "shapes.cpp", `namespace geometry {
class Circle {};
double Circle::area() const { return 1; }
Shape::Shape() {}
}
`)
	ns, ok := syms["geometry"]
	if !ok {
		t.Fatalf("no namespace symbol: %v", syms)
	}
	class, ok := syms["geometry::Circle"]
	if !ok {
		t.Fatalf("no class symbol: %v", syms)
	}
	// Circle is in the file: the out-of-line method belongs to it.
	outOfLine, ok := syms["geometry::Circle::area"]
	if !ok {
		t.Fatalf("Circle::area not qualified geometry::Circle::area: %v", syms)
	}
	if outOfLine.Parent != class.ID {
		t.Errorf("Circle::area parent = %q, want %q", outOfLine.Parent, class.ID)
	}
	// Shape is not: the constructor keeps the namespace as parent but its
	// receiver in the name, so it does not collide with a class geometry::Shape.
	ctor, ok := syms["geometry::Shape::Shape"]
	if !ok {
		t.Fatalf("Shape::Shape not qualified geometry::Shape::Shape: %v", syms)
	}
	if ctor.Parent != ns.ID {
		t.Errorf("Shape::Shape parent = %q, want %q", ctor.Parent, ns.ID)
	}
	if _, ok := syms["geometry::area"]; ok {
		t.Error("geometry::area still present")
	}
}

func TestNestedReceiverRuby(t *testing.T) {
	syms := symbolsByQual(t, "billing.rb", `module Billing
  module Reports; end
  class Reports::Monthly; end
  Gateways::TIMEOUT = 15
  def Billing.configure; end
end
`)
	for _, q := range []string{"Billing::Reports::Monthly", "Billing::Gateways::TIMEOUT", "Billing::configure"} {
		if _, ok := syms[q]; !ok {
			t.Errorf("no symbol %s: %v", q, syms)
		}
	}
	if got, want := syms["Billing::Reports::Monthly"].Parent, syms["Billing::Reports"].ID; got != want {
		t.Errorf("Monthly parent = %q, want %q", got, want)
	}
	if got, want := syms["Billing::Gateways::TIMEOUT"].Parent, syms["Billing"].ID; got != want {
		t.Errorf("TIMEOUT parent = %q, want %q", got, want)
	}
	// A receiver naming the container itself does not repeat it.
	if got, want := syms["Billing::configure"].Parent, syms["Billing"].ID; got != want {
		t.Errorf("configure parent = %q, want %q", got, want)
	}
}

func TestTopLevelReceiverUnchanged(t *testing.T) {
	syms := symbolsByQual(t, "shape.go", `package shape

type Circle struct{}

func (c *Circle) Area() float64 { return 0 }

func (s Square) Side() float64 { return 0 }
`)
	if got, want := syms["shape.Circle.Area"].Parent, syms["shape.Circle"].ID; got == "" || got != want {
		t.Errorf("Area parent = %q, want %q (%v)", got, want, syms)
	}
	if s, ok := syms["shape.Square.Side"]; !ok || s.Parent != "" {
		t.Errorf("Side = %+v, want qualified shape.Square.Side with no parent", s)
	}
}

func TestDefaultSignatureDropsCommentBeforeBody(t *testing.T) {
	syms := symbolsByQual(t, "shop.rb", "module Shop # The currency.\n  X = 1\nend\n")
	if got := syms["Shop"].Signature; got != "module Shop" {
		t.Errorf("signature = %q, want %q", got, "module Shop")
	}
}

func TestStatementPackagesScopeTheirDefinitions(t *testing.T) {
	syms := symbolsByQual(t, "multi.pl", `package Alpha;
sub one { 1 }
package Beta;
sub two { 2 }
our $x = 1;
package main;
sub three { 3 }
`)
	for _, q := range []string{"Alpha::one", "Beta::two", "Beta::x", "main::three"} {
		if _, ok := syms[q]; !ok {
			t.Errorf("no symbol %s: %v", q, keys(syms))
		}
	}
	facts, err := engineForTest(t).Parse("multi.pl", []byte("package Alpha;\nsub one { 1 }\npackage Beta;\n"))
	if err != nil {
		t.Fatal(err)
	}
	if facts.Package != "Alpha" {
		t.Errorf("Package = %q, want the first, Alpha", facts.Package)
	}
}

func TestBlockPackagesKeepTheFirst(t *testing.T) {
	// C#'s block namespaces are not statements: the file's first one
	// qualifies everything, as before.
	syms := symbolsByQual(t, "two.cs", `namespace A { class X {} }
namespace B { class Y {} }
`)
	for _, q := range []string{"A.X", "A.Y"} {
		if _, ok := syms[q]; !ok {
			t.Errorf("no symbol %s: %v", q, keys(syms))
		}
	}
}

func TestQualifiedReceiverIsAbsolute(t *testing.T) {
	syms := symbolsByQual(t, "Circle.pm", `package My::Shape::Circle;
sub My::Shape::Registry::register { 1 }
sub area { 2 }
`)
	for _, q := range []string{"My::Shape::Registry::register", "My::Shape::Circle::area"} {
		if _, ok := syms[q]; !ok {
			t.Errorf("no symbol %s: %v", q, keys(syms))
		}
	}
}

func TestObjCSelectorsJoinNameParts(t *testing.T) {
	src := `@interface Shape : NSObject
- (instancetype)initWithName:(NSString *)name sides:(NSInteger)sides NS_DESIGNATED_INITIALIZER;
- (void)reload;
- (void)setColor:(id)c;
@end
@implementation Shape
- (instancetype)initWithName:(NSString *)name sides:(NSInteger)sides {
  [self.items insertObject:name atIndex:0];
  [self reload];
  [self setColor:nil];
  return self;
}
- (void)tableView:(id)tv didSelect:(id)row {}
- (void)tableView:(id)tv willSelect:(id)row {}
@end
`
	facts, err := engineForTest(t).Parse("Shape.m", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, s := range facts.Symbols {
		ids[s.ID] = true
	}
	for _, id := range []string{"Shape.initWithName:sides:", "Shape.initWithName:sides:~2", "Shape.reload",
		"Shape.setColor:", "Shape.tableView:didSelect:", "Shape.tableView:willSelect:"} {
		if !ids[id] {
			t.Errorf("no symbol %s: %v", id, ids)
		}
	}
	calls := map[string]string{}
	for _, r := range facts.Refs {
		if r.Kind == RefCall {
			calls[r.Name] = r.Qualifier
		}
	}
	for name, qual := range map[string]string{"insertObject:atIndex:": "self.items", "reload": "self", "setColor:": "self"} {
		if got, ok := calls[name]; !ok || got != qual {
			t.Errorf("call %s qualifier = %q (found %v), want %q; calls %v", name, got, ok, qual, calls)
		}
	}
	for _, bad := range []string{"insertObject", "atIndex", ":"} {
		if _, ok := calls[bad]; ok {
			t.Errorf("call %q left over: %v", bad, calls)
		}
	}
}

func TestDefaultSignatureSkipsAnnotationLines(t *testing.T) {
	for _, tc := range []struct{ file, src, qual, want string }{
		{"a.kt", "package p\n\n@Deprecated(\n    \"old\"\n)\n@Suppress(\"x\") fun old(): Int = 1\n", "p.old", `@Suppress("x") fun old(): Int = 1`},
		{"a.kt", "package p\n\n@Deprecated(\"use b\")\nprivate fun a(): Int = 41\n", "p.a", "private fun a(): Int = 41"},
		{"A.java", "package p;\n@Service\n@Transactional(readOnly = \"a)\")\npublic class A {}\n", "p.A", "public class A"},
		{"a.cs", "namespace N;\n[Serializable]\n[Obsolete(\"x]\")]\npublic class C {}\n", "N.C", "public class C"},
		{"a.swift", "@objc final class V {}\n", "V", "@objc final class V"},
	} {
		syms := symbolsByQual(t, tc.file, tc.src)
		if got := syms[tc.qual].Signature; got != tc.want {
			t.Errorf("%s %s signature = %q, want %q (%v)", tc.file, tc.qual, got, tc.want, keys(syms))
		}
	}
}

func TestDocSkipsAttributesAndDecorators(t *testing.T) {
	for _, tc := range []struct{ file, src, qual, want string }{
		{"lib.rs", "//! crate doc\n#![allow(dead_code)]\n\n/// An entry.\n#[derive(Debug)]\n// a note\n#[non_exhaustive]\npub struct Entry;\n", "Entry", "An entry.\na note"},
		{"lib.rs", "//! crate doc\n#![allow(dead_code)]\nfn main() {}\n", "main", ""},
		{"m.py", "# Wraps it.\n@functools.cache\n@other(1)\ndef f():\n    pass\n", "f", "Wraps it."},
		{"a.cs", "namespace N;\n/// <summary>A C.</summary>\n[Serializable]\npublic class C {}\n", "N.C", "<summary>A C.</summary>"},
		{"A.java", "package p;\n/** An A. */\n@Service\npublic class A {}\n", "p.A", "An A."},
		{"a.kt", "package p\n/** Old. */\n@Deprecated(\"x\")\nfun old() = 1\n", "p.old", "Old."},
	} {
		syms := symbolsByQual(t, tc.file, tc.src)
		s, ok := syms[tc.qual]
		if !ok {
			t.Errorf("%s: no symbol %s: %v", tc.file, tc.qual, keys(syms))
			continue
		}
		if s.Doc != tc.want {
			t.Errorf("%s %s doc = %q, want %q", tc.file, tc.qual, s.Doc, tc.want)
		}
	}
}

func keys(m map[string]Symbol) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
