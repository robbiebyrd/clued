// Package voyage is the session.Embedder over Voyage AI's embeddings
// endpoint, with plain net/http. It asks for int8 output at the configured
// dimension; the API key is sent as a bearer token and never appears in
// an error.
package voyage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// The defaults New applies to an empty model and a dimension of 0.
const (
	DefaultURL   = "https://api.voyageai.com/v1/embeddings"
	DefaultModel = "voyage-3.5"
	DefaultDim   = 512
)

// maxAnswerBytes bounds the answer read: 64 vectors of 2,048 numbers in
// JSON fit well under it.
const maxAnswerBytes = 16 << 20

// The fixed errors of a refused call.
var (
	ErrUnauthorized = errors.New("voyage: the API key was refused")
	ErrRateLimited  = errors.New("voyage: rate limited")
)

// Embedder calls the Voyage AI embeddings endpoint.
type Embedder struct {
	// URL is the embeddings endpoint (DefaultURL).
	URL string
	// HTTP is the client used (one with a 60-second timeout by default).
	HTTP *http.Client

	key   string
	model string
	dim   int
}

var _ session.TokenCountingEmbedder = (*Embedder)(nil)

// New returns an embedder for model (DefaultModel when empty) at dim
// numbers per vector (DefaultDim when 0 or less).
func New(apiKey, model string, dim int) *Embedder {
	if model == "" {
		model = DefaultModel
	}
	if dim <= 0 {
		dim = DefaultDim
	}
	return &Embedder{URL: DefaultURL, HTTP: &http.Client{Timeout: 60 * time.Second}, key: apiKey, model: model, dim: dim}
}

// Model is the model name.
func (e *Embedder) Model() string { return e.model }

// Dim is the number of components of each vector.
func (e *Embedder) Dim() int { return e.dim }

// Embed returns one vector per text, in order.
func (e *Embedder) Embed(ctx context.Context, texts []string, kind session.InputKind) ([][]float32, error) {
	vecs, _, err := e.EmbedCounted(ctx, texts, kind)
	return vecs, err
}

type request struct {
	Input           []string `json:"input"`
	Model           string   `json:"model"`
	InputType       string   `json:"input_type"`
	OutputDimension int      `json:"output_dimension"`
	OutputDtype     string   `json:"output_dtype"`
	Truncation      bool     `json:"truncation"`
}

type answer struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
}

// EmbedCounted is Embed that also returns the tokens Voyage counted.
func (e *Embedder) EmbedCounted(ctx context.Context, texts []string, kind session.InputKind) ([][]float32, int, error) {
	if len(texts) == 0 {
		return [][]float32{}, 0, nil
	}
	body, err := json.Marshal(request{Input: texts, Model: e.model, InputType: string(kind), OutputDimension: e.dim, OutputDtype: "int8", Truncation: true})
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.URL, bytes.NewReader(body))
	if err != nil {
		return nil, 0, errors.New("voyage: invalid endpoint")
	}
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", "application/json")
	client := e.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		// The transport error names the URL, never the header.
		return nil, 0, fmt.Errorf("voyage: request failed: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, 0, ErrUnauthorized
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, 0, ErrRateLimited
	case resp.StatusCode != http.StatusOK:
		// The body is not reported: it could echo what was sent.
		return nil, 0, fmt.Errorf("voyage: status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswerBytes))
	if err != nil {
		return nil, 0, fmt.Errorf("voyage: reading the answer: %w", err)
	}
	var a answer
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, 0, errors.New("voyage: the answer is not valid JSON")
	}
	if len(a.Data) != len(texts) {
		return nil, 0, fmt.Errorf("voyage: %d vectors for %d texts", len(a.Data), len(texts))
	}
	out := make([][]float32, len(texts))
	for _, d := range a.Data {
		if d.Index < 0 || d.Index >= len(texts) || out[d.Index] != nil || len(d.Embedding) != e.dim {
			return nil, 0, errors.New("voyage: the answer does not match the request")
		}
		out[d.Index] = d.Embedding
	}
	return out, a.Usage.TotalTokens, nil
}
