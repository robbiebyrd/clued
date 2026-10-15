package memory

import "testing"

func TestStripPrivateNestedAndUnclosed(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"no secrets here", "no secrets here"},
		{"a <private>b</private> c", "a  c"},
		{"<private>all</private>", ""},
		{"x <private>one</private> y <private>two</private> z", "x  y  z"},
		{"a <private>outer <private>inner</private> still private</private> b", "a  b"},
		{"keep <private>the rest\nof the text is gone", "keep "},
		{"a <private>b</private> c <private>unclosed", "a  c "},
		{"a </private> stray close", "a </private> stray close"},
	} {
		if got := StripPrivate(tc.in); got != tc.want {
			t.Errorf("StripPrivate(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
