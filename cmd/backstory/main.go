// Command backstory is the OS memory daemon for Omarchy.
package main

import (
	"fmt"
	"os"

	"github.com/RidgetopAi/backstory/internal/version"
)

const usage = `usage: backstory <command>

commands:
  version   print the build version
  daemon    run the memory daemon (not implemented yet)
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Println(version.Version)
		return 0
	case "daemon":
		fmt.Fprintln(os.Stderr, "backstory daemon: not implemented")
		return 2
	default:
		fmt.Fprintf(os.Stderr, "backstory: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}
