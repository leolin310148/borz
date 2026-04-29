package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

const deprecationNotice = "bb-browser is deprecated; please use 'borz' instead. This wrapper will be removed in a future release."

func main() {
	fmt.Fprintln(os.Stderr, deprecationNotice)

	borzPath, err := exec.LookPath("borz")
	if err != nil {
		fmt.Fprintln(os.Stderr, "bb-browser wrapper: could not find 'borz' on PATH")
		os.Exit(127)
	}

	if err := runBorz(borzPath, os.Args[1:]); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "bb-browser wrapper: %v\n", err)
		os.Exit(1)
	}
}
