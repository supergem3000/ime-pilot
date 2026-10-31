//go:build darwin

package main

import (
	"fmt"
	"os"

	"ime-pilot/internal/bootstrap"
	"ime-pilot/internal/lifecycle"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	release, err := lifecycle.AcquireInstance()
	if err != nil {
		return err
	}
	defer release()
	return bootstrap.Run()
}
