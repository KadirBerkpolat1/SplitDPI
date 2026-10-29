package main

import (
	"os"

	"github.com/KadirBerkpolat1/SplitDPI/internal/cli"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == supervisor.GuardArg {
		os.Exit(supervisor.Guard(os.Args[2:]))
	}
	os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr, ops.New(paths.Under(os.Getenv("SPLITDPI_ROOT")))))
}
