package session

import (
	"container/heap"
	"context"
	"math"
)

// InputKind tells an Embedder what a text is: a document to store or a
// query to search with. Embedding models such as Voyage AI's embed the
// two differently.
type InputKind string

// The input kinds.
const (
	EmbedDocument InputKind = "document"
	EmbedQuery    InputKind = "query"
)

// Embedder is the port to an embedding model: Embed returns one vector of
// Dim numbers per text, in the order of texts. Model names the model (the
// key under which stored vectors are compared: vectors of different
// models are never ranked together).
type Embedder interface {
	Model() string
	Dim() int
	Embed(ctx context.Context, texts []string, kind InputKind) ([][]float32, error)
}

// TokenCountingEmbedder is an Embedder that also reports the tokens the
// model counted for a call, for metering.
type TokenCountingEmbedder interface {
	Embedder
	EmbedCounted(ctx context.Context, texts []string, kind InputKind) (vectors [][]float32, tokens int, err error)
}

// EmbeddingName is the name of the embedding enricher: the key under
// enrichments its Embedding is stored at.
const EmbeddingName = "embedding"

// Embedding is a stored vector: V quantised to int8 with V[i]*Scale the
// original component, Dim its length and Model the model that made it.
// The Mongo store keeps V as a BSON int8 binary vector.
type Embedding struct {
	Model string  `json:"model" bson:"model"`
	Dim   int     `json:"dim" bson:"dim"`
	V     []int8  `json:"v" bson:"v"`
	Scale float32 `json:"scale" bson:"scale"`
}

// Quantize turns a vector into an Embedding of model: each component is
// scaled so the largest magnitude becomes 127 and rounded. A zero vector
// has scale 0.
func Quantize(model string, vec []float32) Embedding {
	var peak float64
	for _, x := range vec {
		peak = math.Max(peak, math.Abs(float64(x)))
	}
	e := Embedding{Model: model, Dim: len(vec), V: make([]int8, len(vec))}
	if peak == 0 {
		return e
	}
	scale := peak / 127
	for i, x := range vec {
		e.V[i] = int8(math.Max(-127, math.Min(127, math.Round(float64(x)/scale))))
	}
	e.Scale = float32(scale)
	return e
}

// CosineInt8 is the cosine similarity of q and the quantised vector v
// (the scale cancels out); 0 when the lengths differ or either is zero.
func CosineInt8(q []float32, v []int8) float64 {
	if len(q) != len(v) || len(q) == 0 {
		return 0
	}
	var dot, nq, nv float64
	for i, x := range q {
		y := float64(v[i])
		dot += float64(x) * y
		nq += float64(x) * float64(x)
		nv += y * y
	}
	if nq == 0 || nv == 0 {
		return 0
	}
	return dot / (math.Sqrt(nq) * math.Sqrt(nv))
}

// TopK keeps the k documents with the highest scores pushed into it.
type TopK struct {
	k int
	h scoredHeap
}

// NewTopK keeps at most k documents (at least one).
func NewTopK(k int) *TopK { return &TopK{k: max(k, 1)} }

// Push offers a document with its score.
func (t *TopK) Push(score float64, d Doc) {
	if len(t.h) < t.k {
		heap.Push(&t.h, scored{score, d})
		return
	}
	if score > t.h[0].score {
		t.h[0] = scored{score, d}
		heap.Fix(&t.h, 0)
	}
}

// Sorted returns the kept documents, highest score first, each with its
// score under "score".
func (t *TopK) Sorted() []Doc {
	out := make([]Doc, len(t.h))
	for i := len(out) - 1; i >= 0; i-- {
		s := heap.Pop(&t.h).(scored)
		s.doc["score"] = s.score
		out[i] = s.doc
	}
	return out
}

type scored struct {
	score float64
	doc   Doc
}

// scoredHeap is a min-heap on score.
type scoredHeap []scored

func (h scoredHeap) Len() int           { return len(h) }
func (h scoredHeap) Less(i, j int) bool { return h[i].score < h[j].score }
func (h scoredHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *scoredHeap) Push(x any)        { *h = append(*h, x.(scored)) }
func (h *scoredHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}
