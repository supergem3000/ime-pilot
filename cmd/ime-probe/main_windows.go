//go:build windows

// ime-probe defaults to read-only cross-process diagnostics. Its explicit
// laboratory mode is only for an independently observed idle test field.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	winime "ime-pilot/internal/platform/windows"
)

func main() {
	pid := flag.Uint("pid", 0, "required foreground target PID")
	delay := flag.Duration("delay", 0, "time to focus the test application")
	labSet := flag.Bool("lab-set-english", false, "laboratory only: send a single English conversion command")
	idle := flag.Bool("lab-idle-confirmed", false, "tester has just independently observed an idle, dedicated test field")
	flag.Parse()
	if *pid == 0 || uint64(*pid) > uint64(^uint32(0)) || *delay < 0 || *delay > 30*time.Second {
		fmt.Fprintln(os.Stderr, "valid -pid is required; -delay must be 0..30s")
		os.Exit(2)
	}
	time.Sleep(*delay)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	report, err := winime.ProbeForeground(ctx, uint32(*pid))
	if err == nil && *labSet {
		report, err = winime.LabSetEnglish(ctx, report, *idle)
	}
	if err != nil {
		report.Outcome = err.Error()
	}
	if e := json.NewEncoder(os.Stdout).Encode(report); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	if err != nil {
		os.Exit(1)
	}
}
