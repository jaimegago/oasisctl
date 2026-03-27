package main

import (
	"os"

	"github.com/jaimegago/oasisctl/internal/cli"
)

func main() {
	if err := cli.NewRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}
