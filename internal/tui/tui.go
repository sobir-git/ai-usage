package tui

import (
	"context"
	"fmt"
	"os"
	"strconv"
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

type inputEvent struct {
	key   byte
	click bool
}

const (
	doubleClickWindow  = 500 * time.Millisecond
	clickRefreshWindow = time.Minute
	maxClickRefreshes  = 1
)

type clickRefreshLimiter struct {
	lastClick time.Time
	accepted  []time.Time
}

func (limiter *clickRefreshLimiter) allow(now time.Time) bool {
	if !limiter.lastClick.IsZero() && now.Sub(limiter.lastClick) >= 0 && now.Sub(limiter.lastClick) <= doubleClickWindow {
		limiter.lastClick = now
		return false
	}
	limiter.lastClick = now
	cutoff := now.Add(-clickRefreshWindow)
	kept := limiter.accepted[:0]
	for _, accepted := range limiter.accepted {
		if accepted.After(cutoff) {
			kept = append(kept, accepted)
		}
	}
	limiter.accepted = kept
	if len(limiter.accepted) >= maxClickRefreshes {
		return false
	}
	limiter.accepted = append(limiter.accepted, now)
	return true
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
	// Use SGR mouse reporting so clicks work through tmux and coordinates do not
	// limit the dashboard size. Releases, rather than presses, trigger refresh.
	fmt.Print("\x1b[?1049h\x1b[?25l\x1b[?1000h\x1b[?1006h")
	defer func() {
		fmt.Print("\x1b[?1006l\x1b[?1000l\x1b[0m\x1b[?25h\x1b[?1049l")
		_ = term.Restore(int(os.Stdin.Fd()), state)
	}()
	events := make(chan inputEvent, 16)
	go readEvents(ctx, events)
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
	var clickLimiter clickRefreshLimiter
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
		case event, ok := <-events:
			if !ok || event.key == 'q' || event.key == 3 || event.key == 4 {
				if remaining > 0 {
					return 0
				}
				return app.ExitCode(results, strict)
			}
			if event.click {
				if remaining == 0 && clickLimiter.allow(time.Now()) {
					start()
				}
				continue
			}
			switch event.key {
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

func readEvents(ctx context.Context, events chan<- inputEvent) {
	defer close(events)
	var parser inputParser
	buffer := make([]byte, 32)
	for {
		n, err := os.Stdin.Read(buffer)
		if err != nil || n == 0 {
			return
		}
		for _, event := range parser.feed(buffer[:n]) {
			select {
			case events <- event:
			case <-ctx.Done():
				return
			}
		}
	}
}

// inputParser separates keyboard input from terminal control sequences. This
// prevents bytes inside mouse reports from accidentally acting as hotkeys.
type inputParser struct {
	state    byte
	mouseBuf []byte
}

func (parser *inputParser) feed(data []byte) []inputEvent {
	var events []inputEvent
	for _, value := range data {
		switch parser.state {
		case 0:
			if value == '\x1b' {
				parser.state = 1
			} else {
				events = append(events, inputEvent{key: value})
			}
		case 1:
			if value == '[' {
				parser.state = 2
			} else {
				parser.state = 0
			}
		case 2:
			if value == '<' {
				parser.state = 3
				parser.mouseBuf = parser.mouseBuf[:0]
			} else if value >= 0x40 && value <= 0x7e {
				parser.state = 0
				if strings.ContainsRune("ABCD", rune(value)) {
					events = append(events, inputEvent{key: value})
				}
			}
		case 3:
			if value == 'M' || value == 'm' {
				if value == 'm' && primaryMouseButton(parser.mouseBuf) {
					events = append(events, inputEvent{click: true})
				}
				parser.state = 0
				parser.mouseBuf = parser.mouseBuf[:0]
			} else if len(parser.mouseBuf) < 64 {
				parser.mouseBuf = append(parser.mouseBuf, value)
			} else {
				parser.state = 0
				parser.mouseBuf = parser.mouseBuf[:0]
			}
		}
	}
	return events
}

func primaryMouseButton(report []byte) bool {
	buttonText, _, ok := strings.Cut(string(report), ";")
	if !ok {
		return false
	}
	button, err := strconv.Atoi(buttonText)
	if err != nil {
		return false
	}
	// Bits 2-4 are modifiers. Reject motion, wheel, and non-primary buttons.
	return button&3 == 0 && button&(32|64|128) == 0
}
