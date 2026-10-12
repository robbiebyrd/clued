package model

import "testing"

func TestCanonicalID(t *testing.T) {
	for _, id := range []string{"0001-abc", "9999-zz9"} {
		got, ok := CanonicalID(id)
		if !ok || got != id {
			t.Errorf("CanonicalID(%q) = %q, %v", id, got, ok)
		}
	}
	for _, id := range []string{"", "0001-ABC", "0001-ab", "0001-abcd", "../x", "0001-abc/", "x"} {
		if got, ok := CanonicalID(id); ok || got != "" {
			t.Errorf("CanonicalID(%q) accepted: %q", id, got)
		}
	}
}

func TestCanonicalTemplateID(t *testing.T) {
	for _, id := range []string{"default", "x", "My.Template_1-a"} {
		got, ok := CanonicalTemplateID(id)
		if !ok || got != id {
			t.Errorf("CanonicalTemplateID(%q) = %q, %v", id, got, ok)
		}
	}
	for _, id := range []string{"", ".", "..", "-x", "a/b", `a\b`, "a:b", "a b", "a\x00"} {
		if got, ok := CanonicalTemplateID(id); ok || got != "" {
			t.Errorf("CanonicalTemplateID(%q) accepted: %q", id, got)
		}
	}
}
