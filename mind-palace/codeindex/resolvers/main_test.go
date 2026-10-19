package resolvers_test

import (
	"os"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// TestMain gives the default engine, which the tests parse their inputs
// with, a generous timeout: under -race on a loaded machine a parse cut off
// after the default 2 s would turn a file into text facts.
func TestMain(m *testing.M) {
	codeparse.SetDefaultTimeout(time.Minute)
	os.Exit(m.Run())
}
