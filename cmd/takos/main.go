package main

import (
	"fmt"
	"os"

	"github.com/busovikov/takos/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "takos:", err)
		os.Exit(1)
	}
}
