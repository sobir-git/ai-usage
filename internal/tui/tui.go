package tui

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/sobir-git/ai-usage/internal/app"
	"golang.org/x/term"
)

func Available() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

// Run keeps input responsive while a refresh is in flight. Pressing r cancels
// the current request set and waits for it to finish before starting another;
// this prevents overlapping refreshes and keeps q responsive.
func Run(ctx context.Context, selected []app.ProviderSpec, timeout time.Duration, retries int, showMissing, strict bool) int {
	inputFD := int(os.Stdin.Fd())
	state, err := term.MakeRaw(inputFD)
	if err != nil {
		return runOnceFallback(ctx, selected, timeout, retries, showMissing, strict)
	}
	defer func() {
		_ = term.Restore(inputFD, state)
		fmt.Fprintln(os.Stdout)
	}()

	keys := make(chan byte, 16)
	go readKeys(ctx, keys)

	var latest []app.ProviderResult
	for {
		clearScreen()
		fmt.Fprintln(os.Stdout, "AI usage")
		fmt.Fprintln(os.Stdout, "Refreshing...")
		fmt.Fprintln(os.Stdout)
		flush()

		requestCtx, cancel := context.WithCancel(ctx)
		done := make(chan []app.ProviderResult, 1)
		go func() {
			done <- app.Collect(requestCtx, selected, timeout, retries)
		}()

		refreshAgain := false
	collecting:
		for {
			select {
			case results := <-done:
				latest = results
				cancel()
				break collecting
			case key, ok := <-keys:
				if !ok || key == 'q' || key == 3 {
					cancel()
					return 0
				}
				if key == 'r' {
					refreshAgain = true
					cancel()
					<-done
					break collecting
				}
			case <-ctx.Done():
				cancel()
				return 0
			}
		}

		if refreshAgain {
			continue
		}

		clearScreen()
		fmt.Fprintln(os.Stdout, app.FormatHuman(latest, showMissing))
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "[r] refresh  [q] quit")
		flush()
	waiting:
		for {
			select {
			case key, ok := <-keys:
				if !ok || key == 'q' || key == 3 {
					return app.ExitCode(latest, strict)
				}
				if key == 'r' {
					refreshAgain = true
					break waiting
				}
			case <-ctx.Done():
				return 0
			}
		}
		if refreshAgain {
			continue
		}
	}
}

func runOnceFallback(ctx context.Context, selected []app.ProviderSpec, timeout time.Duration, retries int, showMissing, strict bool) int {
	results := app.Collect(ctx, selected, timeout, retries)
	fmt.Fprintln(os.Stdout, app.FormatHuman(results, showMissing))
	return app.ExitCode(results, strict)
}

func readKeys(ctx context.Context, keys chan<- byte) {
	defer close(keys)
	buffer := make([]byte, 1)
	for {
		count, err := os.Stdin.Read(buffer)
		if err != nil || count == 0 {
			return
		}
		for _, key := range buffer[:count] {
			if key >= 'A' && key <= 'Z' {
				key += 'a' - 'A'
			}
			select {
			case keys <- key:
			case <-ctx.Done():
				return
			}
		}
	}
}

func clearScreen() {
	fmt.Fprint(os.Stdout, "\x1b[2J\x1b[H")
}

func flush() {
	_ = os.Stdout.Sync()
}
