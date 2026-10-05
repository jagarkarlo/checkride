package main

import (
	"os"

	"github.com/jagarkarlo/nostekon/internal/attest"
)

func main() {
	os.Exit(attest.Run(os.Args[1:], os.Stdout, os.Stderr))
}
