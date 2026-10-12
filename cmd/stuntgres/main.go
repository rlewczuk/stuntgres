package main

import (
	"fmt"
	"os"
)

func main() {
	if err := runCLI(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "stuntgres:", err)
		os.Exit(1)
	}
}
