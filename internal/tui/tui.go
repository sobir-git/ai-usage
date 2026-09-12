package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sobir-git/ai-usage/internal/app"
	"golang.org/x/term"
)

func Available() bool {
	return os.Getenv("TERM") != "dumb" && term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

type update struct {
	index  int
	result app.ProviderResult
}

// Run owns the screen; workers only send immutable provider results. Refresh
// keystrokes are ignored while fetching so they cannot restart or stack requests.
func Run(ctx context.Context, selected []app.ProviderSpec, timeout time.Duration, retries int, showMissing, strict bool) int {
	state, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		results := app.Collect(ctx, selected, timeout, retries)
		fmt.Println(app.FormatHuman(results, showMissing))
		return app.ExitCode(results, strict)
	}
	ctx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer func() {
		cancel()
		// Give app-server workers time to reap their child processes before
		// main calls os.Exit, while keeping quit bounded for a stuck provider.
		done := make(chan struct{})
		go func() { workers.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(500 * time.Millisecond):
		}
	}()
	fmt.Print("\x1b[?1049h\x1b[?25l")
	defer func() {
		fmt.Print("\x1b[0m\x1b[?25h\x1b[?1049l")
		_ = term.Restore(int(os.Stdin.Fd()), state)
	}()
	keys := make(chan byte, 16)
	go readKeys(ctx, keys)
	updates := make(chan update, len(selected))
	results := make([]app.ProviderResult, len(selected))
	pending := make([]bool, len(selected))
	for i, spec := range selected {
		results[i] = app.ProviderResult{ID: spec.ID, Name: spec.Name, Status: "loading"}
	}
	remaining := 0
	var refreshed time.Time
	start := func() {
		remaining = len(selected)
		for i, spec := range selected {
			pending[i] = true
			workers.Add(1)
			go func(i int, spec app.ProviderSpec) {
				defer workers.Done()
				result := app.Collect(ctx, []app.ProviderSpec{spec}, timeout, retries)[0]
				select {
				case updates <- update{i, result}:
				case <-ctx.Done():
				}
			}(i, spec)
		}
	}
	start()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	offset := 0
	previousFrame := ""
	for {
		width, height, err := term.GetSize(int(os.Stdout.Fd()))
		if err != nil || width < 1 || height < 1 {
			width, height = 80, 24
		}
		frame, nextOffset := render(results, pending, showMissing, refreshed, width, height, offset, time.Now(), os.Getenv("NO_COLOR") == "")
		offset = nextOffset
		// Raw mode disables LF -> CRLF conversion. Build the whole frame before
		// writing to avoid staircase text and clear-screen flicker.
		if frame != previousFrame {
			fmt.Print("\x1b[H", strings.ReplaceAll(frame, "\n", "\r\n"), "\x1b[J")
			previousFrame = frame
		}
		select {
		case <-ctx.Done():
			return 0
		case key, ok := <-keys:
			if !ok || key == 'q' || key == 3 || key == 4 {
				if remaining > 0 {
					return 0
				}
				return app.ExitCode(results, strict)
			}
			switch key {
			case 'r':
				if remaining == 0 {
					start()
				}
			case 'j', 'B':
				offset++
			case 'k', 'A':
				offset--
			case ' ':
				offset += max(1, height-6)
			case 'g':
				offset = 0
			case 'G':
				offset = 1 << 30
			}
		case value := <-updates:
			results[value.index] = value.result
			pending[value.index] = false
			remaining--
			if remaining == 0 {
				refreshed = time.Now()
			}
		case <-ticker.C:
		}
	}
}

func readKeys(ctx context.Context, keys chan<- byte) {
	defer close(keys)
	buffer := make([]byte, 32)
	for {
		n, err := os.Stdin.Read(buffer)
		if err != nil || n == 0 {
			return
		}
		for _, key := range buffer[:n] {
			select {
			case keys <- key:
			case <-ctx.Done():
				return
			}
		}
	}
}
