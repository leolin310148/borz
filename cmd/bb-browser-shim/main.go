package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

const deprecationNotice = "bb-browser is deprecated; please use 'borz' instead. This wrapper will be removed in a future release."

var (
	lookPath       = exec.LookPath
	executablePath = os.Executable
	runBorz        = runBorzExec
)

func main() {
	os.Exit(runWrapper(os.Args[1:], os.Stderr))
}

func runWrapper(args []string, stderr io.Writer) int {
	fmt.Fprintln(stderr, deprecationNotice)

	borzPath, err := lookPath("borz")
	if err != nil {
		fmt.Fprintln(stderr, "bb-browser wrapper: could not find 'borz' on PATH")
		return 127
	}
	if sameExecutable(borzPath) {
		fmt.Fprintln(stderr, "bb-browser wrapper: 'borz' on PATH resolves to this compatibility wrapper")
		return 126
	}

	if err := runBorz(borzPath, args); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(stderr, "bb-browser wrapper: %v\n", err)
		return 1
	}
	return 0
}

func sameExecutable(path string) bool {
	self, err := executablePath()
	if err != nil {
		return false
	}
	selfInfo, err := os.Stat(self)
	if err != nil {
		return false
	}
	targetInfo, err := os.Stat(path)
	if err != nil {
		return false
	}
	return os.SameFile(selfInfo, targetInfo)
}
