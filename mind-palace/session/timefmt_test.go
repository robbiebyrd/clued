package session_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

type timeHolder struct {
	At      time.Time   `json:"at"`
	Maybe   *time.Time  `json:"maybe"`
	Skipped string      `json:"-"`
	Empty   []string    `json:"empty,omitempty"`
	Nested  session.Doc `json:"nested"`
}

func TestJSONTimesFormatsEveryTimeWithThreeFractionalDigits(t *testing.T) {
	at := time.Date(2026, 10, 11, 17, 29, 21, 760_000_000, time.FixedZone("x", -5*3600))
	whole := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	holder := timeHolder{At: at, Maybe: &whole, Skipped: "no", Nested: session.Doc{"when": whole}}
	in := map[string]any{
		"doc":    session.Doc{"t": at, "list": []any{at, "x", 3}},
		"docs":   []session.Doc{{"t": whole}},
		"struct": []timeHolder{holder},
		"nilptr": (*time.Time)(nil),
		"nilsl":  []session.Doc(nil),
		"plain":  1.5,
	}

	b, err := json.Marshal(session.JSONTimes(in))
	if err != nil {
		t.Fatal(err)
	}

	want := `{"doc":{"list":["2026-10-11T22:29:21.760Z","x",3],"t":"2026-10-11T22:29:21.760Z"},` +
		`"docs":[{"t":"2026-01-02T03:04:05.000Z"}],"nilptr":null,"nilsl":null,"plain":1.5,` +
		`"struct":[{"at":"2026-10-11T22:29:21.760Z","maybe":"2026-01-02T03:04:05.000Z","nested":{"when":"2026-01-02T03:04:05.000Z"}}]}`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
}

func TestJSONTimesPromotesEmbeddedStructFields(t *testing.T) {
	type inner struct {
		A  int       `json:"a"`
		B  string    `json:"b"`
		At time.Time `json:"at"`
	}
	type outer struct {
		B string `json:"b"`
		inner
		Named inner `json:"named"`
	}
	got := session.JSONTimes(outer{B: "outer", inner: inner{A: 1, B: "inner", At: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}})
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":1,"at":"2026-01-02T03:04:05.000Z","b":"outer","named":{"a":0,"at":"0001-01-01T00:00:00.000Z","b":""}}`
	if string(b) != want {
		t.Errorf("JSONTimes = %s, want %s", b, want)
	}
}
