package voyage_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/embed/voyage"
)

const secret = "pa-test-secret-key-0123456789"

var _ session.TokenCountingEmbedder = (*voyage.Embedder)(nil)

type request struct {
	Input           []string `json:"input"`
	Model           string   `json:"model"`
	InputType       string   `json:"input_type"`
	OutputDimension int      `json:"output_dimension"`
	OutputDtype     string   `json:"output_dtype"`
}

// fake answers like the embeddings endpoint: one int8 vector per input,
// listed in reverse to check that the adapter orders by index.
func fake(t *testing.T, got *request, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/embeddings" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer "+secret {
			t.Errorf("Authorization = %q", auth)
		}
		if err := json.NewDecoder(r.Body).Decode(got); err != nil {
			t.Error(err)
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"detail":"bad key ` + secret + `"}`))
			return
		}
		type item struct {
			Embedding []int `json:"embedding"`
			Index     int   `json:"index"`
		}
		var data []item
		for i := len(got.Input) - 1; i >= 0; i-- {
			v := make([]int, got.OutputDimension)
			v[i%len(v)] = 100 + i
			data = append(data, item{v, i})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data, "model": got.Model, "usage": map[string]any{"total_tokens": 42}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRequestCarriesKindDimensionAndInt8(t *testing.T) {
	for _, kind := range []session.InputKind{session.EmbedDocument, session.EmbedQuery} {
		var got request
		srv := fake(t, &got, http.StatusOK)
		e := voyage.New(secret, "voyage-3.5", 4)
		e.URL = srv.URL + "/v1/embeddings"
		vecs, tokens, err := e.EmbedCounted(context.Background(), []string{"alpha", "beta"}, kind)
		if err != nil {
			t.Fatal(err)
		}
		if got.InputType != string(kind) || got.OutputDimension != 4 || got.OutputDtype != "int8" || got.Model != "voyage-3.5" {
			t.Errorf("request = %+v", got)
		}
		if len(vecs) != 2 || vecs[0][0] != 100 || vecs[1][1] != 101 {
			t.Errorf("vectors not in input order: %v", vecs)
		}
		if tokens != 42 {
			t.Errorf("tokens = %d", tokens)
		}
	}
}

func TestDefaultsAndEmptyInput(t *testing.T) {
	e := voyage.New(secret, "", 0)
	if e.Model() != voyage.DefaultModel || e.Dim() != voyage.DefaultDim {
		t.Fatalf("defaults = %s/%d", e.Model(), e.Dim())
	}
	e.URL = "http://127.0.0.1:1/never"
	vecs, err := e.Embed(context.Background(), nil, session.EmbedDocument)
	if err != nil || len(vecs) != 0 {
		t.Fatalf("empty input = %v, %v", vecs, err)
	}
}

func TestUnauthorizedIsAFixedErrorWithoutTheKey(t *testing.T) {
	var got request
	srv := fake(t, &got, http.StatusUnauthorized)
	e := voyage.New(secret, "voyage-3.5", 4)
	e.URL = srv.URL + "/v1/embeddings"
	_, err := e.Embed(context.Background(), []string{"alpha"}, session.EmbedDocument)
	if !errors.Is(err, voyage.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaks the key: %v", err)
	}
}

func TestOtherFailuresNeverCarryTheKey(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusTooManyRequests, http.StatusInternalServerError} {
		var got request
		srv := fake(t, &got, status)
		e := voyage.New(secret, "voyage-3.5", 4)
		e.URL = srv.URL + "/v1/embeddings"
		_, err := e.Embed(context.Background(), []string{"alpha"}, session.EmbedDocument)
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatalf("status %d: err = %v", status, err)
		}
		if status == http.StatusTooManyRequests && !errors.Is(err, voyage.ErrRateLimited) {
			t.Errorf("429: err = %v, want ErrRateLimited", err)
		}
	}
	// An unreachable endpoint: the transport error names no key either.
	e := voyage.New(secret, "voyage-3.5", 4)
	e.URL = "http://127.0.0.1:1/v1/embeddings"
	if _, err := e.Embed(context.Background(), []string{"alpha"}, session.EmbedDocument); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("unreachable: err = %v", err)
	}
}

func TestMismatchedAnswerIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"embedding":[1,2],"index":0}],"usage":{"total_tokens":1}}`))
	}))
	defer srv.Close()
	e := voyage.New(secret, "voyage-3.5", 4)
	e.URL = srv.URL
	if _, err := e.Embed(context.Background(), []string{"a", "b"}, session.EmbedDocument); err == nil {
		t.Fatal("an answer with too few or short vectors must fail")
	}
}
