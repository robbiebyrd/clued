package session

import (
	"math"
	"testing"
)

func TestQuantizeKeepsDirection(t *testing.T) {
	vec := []float32{0.5, -1, 0.25, 0}
	e := Quantize("m", vec)
	if e.Model != "m" || e.Dim != 4 || e.V[1] != -127 || e.V[0] != 64 {
		t.Fatalf("Quantize = %+v", e)
	}
	if got := CosineInt8(vec, e.V); math.Abs(got-1) > 1e-3 {
		t.Fatalf("cosine with itself = %v", got)
	}
	if zero := Quantize("m", []float32{0, 0}); zero.Scale != 0 || zero.V[0] != 0 {
		t.Fatalf("zero vector = %+v", zero)
	}
	if CosineInt8([]float32{1}, []int8{1, 2}) != 0 || CosineInt8([]float32{0, 0}, []int8{1, 2}) != 0 {
		t.Fatal("mismatched or zero vectors must score 0")
	}
}

func TestTopKKeepsTheBest(t *testing.T) {
	top := NewTopK(2)
	for i, s := range []float64{0.1, 0.9, 0.5, 0.7} {
		top.Push(s, Doc{"i": i})
	}
	got := top.Sorted()
	if len(got) != 2 || got[0]["i"] != 1 || got[1]["i"] != 3 || got[0]["score"] != 0.9 {
		t.Fatalf("Sorted = %v", got)
	}
}
