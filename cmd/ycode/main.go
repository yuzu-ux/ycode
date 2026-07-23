package main

import (
	"os"

	"github.com/yuzu-ux/ycode/internal/cli"
)

var version = "0.1.0-dev"

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, version))
}
