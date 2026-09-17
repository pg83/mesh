//go:build meshcoverage

package main

import (
	"os"
	"runtime/coverage"
)

func flushCoverage() {
	coverage.WriteCountersDir(os.Getenv("GOCOVERDIR"))
}
