package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sobir-git/ai-usage/internal/app"
	"github.com/sobir-git/ai-usage/internal/providers"
	"github.com/sobir-git/ai-usage/internal/tui"
)

var version = providers.ToolVersion

type providerFlags []string

func (flags *providerFlags) String() string {
	if flags == nil {
		return ""
	}
	return fmt.Sprint([]string(*flags))
}

func (flags *providerFlags) Set(value string) error {
	*flags = append(*flags, value)
	return nil
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(arguments []string) int {
	flagSet := flag.NewFlagSet("ai-usage", flag.ContinueOnError)
	flagSet.SetOutput(os.Stderr)
	flagSet.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: ai-usage [options]")
		fmt.Fprintln(os.Stderr, "Show live usage for configured Devin, Codex, and Claude Code accounts.")
		flagSet.PrintDefaults()
	}
	var selectedFlags providerFlags
	flagSet.Var(&selectedFlags, "provider", "limit the check to devin, codex, or claude; repeat for multiple providers")
	jsonOutput := flagSet.Bool("json", false, "print a normalized JSON snapshot")
	once := flagSet.Bool("once", false, "print one snapshot instead of opening the TUI")
	showMissing := flagSet.Bool("show-missing", false, "show providers without local credentials")
	strict := flagSet.Bool("strict", false, "return failure when any configured account cannot be read")
	timeoutSeconds := flagSet.Float64("timeout", 8, "per-attempt network timeout in seconds")
	retries := flagSet.Int("retries", 2, "transient-failure retries")
	showVersion := flagSet.Bool("version", false, "print the version")
	if err := flagSet.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flagSet.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "ai-usage: unexpected argument:", flagSet.Arg(0))
		return 2
	}
	if *showVersion {
		fmt.Printf("ai-usage %s\n", version)
		return 0
	}
	if math.IsNaN(*timeoutSeconds) || math.IsInf(*timeoutSeconds, 0) || *timeoutSeconds < 0.001 || *timeoutSeconds > 3600 {
		fmt.Fprintln(os.Stderr, "ai-usage: --timeout must be between 0.001 and 3600 seconds")
		return 2
	}
	if *retries < 0 {
		fmt.Fprintln(os.Stderr, "ai-usage: --retries cannot be negative")
		return 2
	}
	selected, err := app.SelectProviders(selectedFlags)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ai-usage:", err)
		return 2
	}
	timeout := time.Duration(*timeoutSeconds * float64(time.Second))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if !*jsonOutput && !*once && tui.Available() {
		return tui.Run(ctx, selected, timeout, *retries, *showMissing, *strict)
	}
	results := app.Collect(ctx, selected, timeout, *retries)
	if *jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(app.MakeSnapshot(results)); err != nil {
			fmt.Fprintln(os.Stderr, "ai-usage: could not encode JSON:", err)
			return 1
		}
	} else {
		fmt.Println(app.FormatHuman(results, *showMissing))
	}
	return app.ExitCode(results, *strict)
}
