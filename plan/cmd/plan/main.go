// Command plan is the command line entrypoint of the plan service.
package main

import (
	"os"

	"github.com/robbiebyrd/clued/plan/cli"

	// Storage plugins register themselves on import.
	_ "github.com/robbiebyrd/clued/plan/store/filestore"
	_ "github.com/robbiebyrd/clued/plan/store/memstore"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
