package memcode_test

import (
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/code/codetest"
	"github.com/robbiebyrd/clued/mind-palace/code/memcode"
)

func TestConformance(t *testing.T) {
	codetest.Run(t, func(t *testing.T) code.Store { return memcode.New() })
}
