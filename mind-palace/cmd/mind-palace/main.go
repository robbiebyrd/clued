// Command mind-palace is the command line entrypoint of the plan and story
// services.
package main

import (
	"os"

	"github.com/robbiebyrd/clued/mind-palace/cli"

	// Storage plugins register themselves on import.
	_ "github.com/robbiebyrd/clued/mind-palace/store/filestore"
	_ "github.com/robbiebyrd/clued/mind-palace/store/firestorestore"
	_ "github.com/robbiebyrd/clued/mind-palace/store/memstore"
	_ "github.com/robbiebyrd/clued/mind-palace/store/mongostore"
	_ "github.com/robbiebyrd/clued/mind-palace/store/sqlstore"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
