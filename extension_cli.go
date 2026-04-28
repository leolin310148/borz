package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/leolin310148/bb-browser-go/internal/config"
	"github.com/leolin310148/bb-browser-go/internal/extupdate"
)

// extensionDir is where the bb-browser Chrome extension is extracted to.
// User loads it via chrome://extensions → "Load unpacked" → this path.
func extensionDir() string {
	return filepath.Join(config.HomeDir(), "extension")
}

func handleExtension(cmdArgs []string) {
	sub := "download"
	if len(cmdArgs) > 0 {
		sub = cmdArgs[0]
	}
	switch sub {
	case "download", "update", "install":
		runExtensionDownload()
	case "path":
		fmt.Println(extensionDir())
	default:
		fmt.Fprintf(os.Stderr, "Unknown 'extension' subcommand: %s\n", sub)
		fmt.Fprintln(os.Stderr, "Run 'bb-browser help extension' for usage.")
		os.Exit(1)
	}
}

func runExtensionDownload() {
	dir := extensionDir()
	res, err := extupdate.Run(context.Background(), extupdate.Options{
		DestDir: dir,
	})
	if err != nil {
		fatal(err.Error())
	}
	printExtensionSetupHint(res.Tag, res.DestDir)
}

func printExtensionSetupHint(tag, dir string) {
	fmt.Println()
	fmt.Printf("bb-browser extension %s installed to:\n  %s\n", tag, dir)
	fmt.Println()
	fmt.Println("Load it into Chrome:")
	fmt.Println("  1. Open chrome://extensions")
	fmt.Println("  2. Enable \"Developer mode\" (top-right)")
	fmt.Println("  3. Click \"Load unpacked\" and select the directory above")
	fmt.Println()
	fmt.Println("Re-run 'bb-browser extension update' to upgrade after a new release.")
}
