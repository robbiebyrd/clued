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
