//go:build tools

// Package tools pins Go developer tools used by this repository. The blank
// imports record each tool as a module requirement so `go run <import path>`
// (or `go install <import path>`) always uses the pinned version, keeping CI
// and local development reproducible.
package tools

import (
	_ "github.com/golangci/golangci-lint/v2/cmd/golangci-lint"
	_ "golang.org/x/tools/go/analysis/passes/modernize/cmd/modernize"
	_ "golang.org/x/vuln/cmd/govulncheck"
)
