package main

import (
	"fmt"
	"os"

	"github.com/leolin310148/borz/internal/diagnostics"
)

// runDoctor performs end-to-end diagnostics for the CLI/daemon/browser stack.
// Exits 0 when all checks pass, 1 when any check fails (warn does not fail).
func runDoctor(jsonOutput bool) {
	checks, ok := diagnostics.Run(version)
	if jsonOutput {
		fmt.Println(diagnostics.RenderJSON(checks))
	} else {
		fmt.Print(diagnostics.RenderText(checks))
	}
	if !ok {
		os.Exit(1)
	}
}
