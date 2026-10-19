package memgraph_test

import (
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/codegraph"
	"github.com/robbiebyrd/clued/mind-palace/codegraph/codegraphtest"
	"github.com/robbiebyrd/clued/mind-palace/codegraph/memgraph"
)

func TestSuite(t *testing.T) {
	codegraphtest.Run(t, func(*testing.T) codegraph.Opener { return memgraph.New() })
}
