package main

import (
	"errors"
	"fmt"
	"os"
)

type exitCoder interface {
	ExitCode() int
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		var codeErr exitCoder
		if errors.As(err, &codeErr) {
			os.Exit(codeErr.ExitCode())
		}
		if outputJSON {
			_ = emitJSON(map[string]any{"ok": false, "error": err.Error()})
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
