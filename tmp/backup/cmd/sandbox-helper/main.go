package main

import (
	"fmt"
	"os"

	"github.com/OnslaughtSnail/caelis/internal/sandboxhelper"
)

func main() {
	if sandboxhelper.MaybeRun(os.Args[1:]) {
		return
	}
	fmt.Fprintln(os.Stderr, "unsupported sandbox helper command")
	os.Exit(2)
}
