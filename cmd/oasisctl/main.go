package main

import (
	"fmt"
	"os"

	"github.com/jaimegago/oasisctl/internal/cli"
)

func main() {
	if err := cli.NewRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
