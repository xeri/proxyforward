// Package doccheck contains no production code. Its tests keep the agent
// documentation honest: every file, symbol, and test name cited by CLAUDE.md,
// docs/agent/, .claude/rules/, and .claude/skills/ must exist in the working
// tree, so a rename or deletion fails `go test ./...` instead of silently
// rotting the docs. The citation grammar the tests understand is documented
// at the top of citations_test.go.
package doccheck
