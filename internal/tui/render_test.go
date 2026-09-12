package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/sobir-git/ai-usage/internal/app"
	"github.com/sobir-git/ai-usage/internal/model"
)

func fixture(now time.Time) []app.ProviderResult {
	reset := now.Add(2*time.Hour + 14*time.Minute).Format(time.RFC3339)
	percent := 75.0
	plan := "Pro"
	return []app.ProviderResult{
		{ID: "devin", Usage: &model.DevinUsage{Plan: &plan, DailyRemainingPercent: &percent, DailyResetAt: &reset, WeeklyRemainingPercent: &percent}},
		{ID: "codex", Usage: &model.CodexAggregate{Accounts: []model.CodexAccount{
			{Profile: "default", Status: "ok", Usage: &model.CodexUsage{Plan: &plan, Windows: []model.Window{{Name: "5h", RemainingPercent: 90, ResetAt: &reset}, {Name: "GPT-5.3-Codex-Spark (5h)", RemainingPercent: 100}}}},
			{Profile: "codex-2", Status: "error", Error: "Login expired; log in again"},
		}}},
		{ID: "claude", Name: "Claude Code", Status: "error", Error: "Claude Code is rate limited (HTTP 429); try again later"},
	}
}

func TestViewportBoundsAndScrolling(t *testing.T) {
	now := time.Now()
	for _, size := range [][2]int{{100, 30}, {80, 24}, {40, 12}, {20, 8}, {5, 5}, {1, 1}} {
		for _, offset := range []int{-20, 0, 10000} {
			frame, actual := render(fixture(now), nil, false, now, size[0], size[1], offset, now, false)
			rows := strings.Split(frame, "\n")
			if len(rows) > size[1] || actual < 0 {
				t.Fatalf("viewport overflow at %v: %d rows, offset %d", size, len(rows), actual)
			}
			for _, row := range rows {
				if len(strings.ReplaceAll(row, "\x1b[K", "")) > max(1, size[0]-1) {
					t.Fatalf("row wraps at %v: %q", size, row)
				}
			}
		}
	}
}

func TestQuotaAndRefreshDisplay(t *testing.T) {
	now := time.Now()
	frame, _ := render(fixture(now), []bool{true, false, false}, false, now, 100, 30, 0, now, false)
	for _, want := range []string{"75.0% left", "resets in 2h 14m", "[refreshing]", "codex-2", "HTTP 429", "q quit"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("missing %q:\n%s", want, frame)
		}
	}
	if strings.Contains(frame, "Spark") {
		t.Fatal("Spark leaked into dashboard")
	}
	if got := clean("plan\x1b\n\r\tname"); strings.ContainsAny(got, "\x1b\n\r\t") {
		t.Fatal("terminal control injection")
	}
}

func TestResetLabelUsesLocalTimeAndDoesNotInventReset(t *testing.T) {
	now := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	reset := now.Add(13 * time.Hour).Format(time.RFC3339)
	expected := "resets " + now.Add(13*time.Hour).In(time.Local).Format("Jan 2, 3:04 PM")
	if got := resetLabel(&reset, now); got != expected {
		t.Fatalf("got %q want %q", got, expected)
	}
	if got := resetLabel(&reset, now.Add(14*time.Hour)); got != "reset due; refresh to confirm" {
		t.Fatal(got)
	}
}
