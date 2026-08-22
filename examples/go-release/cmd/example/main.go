package main

import (
	"flag"
	"fmt"
	"os"
)

var (
	version = "dev"
	commit  = "none"
)

// main is the process entrypoint.
func main() {
	os.Exit(run())
}

// run parses flags and prints identity or a one-line description.
func run() int {
	showVersion := flag.Bool("version", false, "print the project version and commit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("example %s (%s)\n", version, commit)
		return 0
	}

	fmt.Println("example is a copyable reusable-workflow consumer.")
	return 0
}
