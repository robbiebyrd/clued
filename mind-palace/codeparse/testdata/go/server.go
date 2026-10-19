// Copyright 2026 Example. A file header, not a doc comment.

// Package demo serves things.
package demo

import (
	"context"
	"fmt"
	str "strings"

	_ "embed"
	. "math"
)

import "io"

// DefaultName is the name a Server gets.
const DefaultName = "demo"

const (
	// MaxConns bounds connections.
	MaxConns, minConns = 64, 1
	_                  = 0
)

var (
	registry = map[string]*Server{}
	// ErrClosed is returned after Close.
	ErrClosed = fmt.Errorf("closed")
)

var _ Handler = (*Server)(nil)

// Server serves requests.
//
// It is safe for concurrent use.
type Server struct {
	// Name names the server.
	Name string
	n, m int
	*Embedded
	io.Reader
	opts Options[string] `json:"opts"`
}

type (
	// ID identifies a server.
	ID int

	// Handler handles one request.
	Handler interface {
		// Serve serves x.
		Serve(ctx context.Context, x int) error
		fmt.Stringer
	}
)

// Options are generic options.
type Options[T any] struct {
	Value T
}

// Alias is another name for a map.
type Alias = map[string]int

/*
NewServer returns a server.
It registers it too.
*/
func NewServer(name string) *Server {
	s := &Server{Name: str.ToUpper(name)}
	registry[name] = s
	return s
}

// Serve implements Handler.
func (s *Server) Serve(ctx context.Context, x int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.helper(x)
	fmt.Println(Sqrt(float64(x)))
	return nil
}

func (s Server) String() string { return s.Name }

func (s *Server) helper(n int) int {
	return first[int]([]int{n})
}

// Get returns the value.
func (o *Options[T]) Get() T { return o.Value }

func first[T any](xs []T) T {
	var zero T
	if len(xs) == 0 {
		return zero
	}
	return xs[0]
}

func init() {
	NewServer(DefaultName)
}

func init() {
	_ = MaxConns
}

// Trailing comment at the end of the file.
