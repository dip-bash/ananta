package main

import (
	"fmt"
	"os"

	"ananta/internal/ananta"
)

func main() {
	if err := ananta.Run(os.Stdin, os.Stdout, os.Stderr, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
