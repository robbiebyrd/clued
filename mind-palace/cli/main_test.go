package cli

import (
	"os"
	"strings"
	"testing"
)

// TestMain runs the tests without the machine's CLUED_* variables. In a
// container linked to a clued cloud, CLUED_SERVER_URL and CLUED_SERVER_KEY
// would otherwise send the tests' config reads, logins and relays to that
// server with the container's real key; tests that want a variable set it
// with t.Setenv.
func TestMain(m *testing.M) {
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "CLUED_") {
			os.Unsetenv(name)
		}
	}
	os.Exit(m.Run())
}
