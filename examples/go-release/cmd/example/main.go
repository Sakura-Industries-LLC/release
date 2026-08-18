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

func main() {
	os.Exit(run())
}

func run() int {
	showVersion := flag.Bool("version", false, "print the project version and commit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("example %s (%s)\n", version, commit)
		return 0
	}

	fmt.Println("example is a copyable Meigma GitHub Release consumer.")
	return 0
}
