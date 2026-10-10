// Command story is the story-only entrypoint: `story <command>` runs
// `mind-palace story <command>`; the top-level commands (serve, mcp, sync…)
// pass through.
package main

import (
	"os"

	"github.com/robbiebyrd/clued/mind-palace/cli"
	"github.com/robbiebyrd/clued/mind-palace/kind"

	// Storage plugins register themselves on import.
	_ "github.com/robbiebyrd/clued/mind-palace/store/filestore"
	_ "github.com/robbiebyrd/clued/mind-palace/store/firestorestore"
	_ "github.com/robbiebyrd/clued/mind-palace/store/memstore"
	_ "github.com/robbiebyrd/clued/mind-palace/store/mongostore"
	_ "github.com/robbiebyrd/clued/mind-palace/store/sqlstore"
)

func main() {
	os.Exit(cli.MainKind(kind.Story, os.Args[1:]))
}
