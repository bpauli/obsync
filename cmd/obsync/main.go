package main

import (
	"fmt"
	"os"

	"obsync/internal/cmd"
)

func main() {
	if err := cmd.Execute(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(cmd.ExitCode(err))
	}
}
