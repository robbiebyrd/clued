// Command plan is the plan-only entrypoint: `plan <command>` runs
// `mind-palace plan <command>`; the top-level commands (serve, mcp, sync…)
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
	os.Exit(cli.MainKind(kind.Plan, os.Args[1:]))
}
