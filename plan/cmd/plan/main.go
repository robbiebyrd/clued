// Command plan is the command line entrypoint of the plan service.
package main

import (
	"os"

	"github.com/robbiebyrd/clued/plan/cli"

	// Storage plugins register themselves on import.
	_ "github.com/robbiebyrd/clued/plan/store/filestore"
	_ "github.com/robbiebyrd/clued/plan/store/firestorestore"
	_ "github.com/robbiebyrd/clued/plan/store/memstore"
	_ "github.com/robbiebyrd/clued/plan/store/mongostore"
	_ "github.com/robbiebyrd/clued/plan/store/sqlstore"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
