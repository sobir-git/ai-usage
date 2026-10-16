package tui

import (
	"testing"
	"time"
)

func TestInputParserRecognizesPrimaryReleaseAcrossReads(t *testing.T) {
	var parser inputParser
	events := parser.feed([]byte("r\x1b[<0;12"))
	if len(events) != 1 || events[0].key != 'r' {
		t.Fatalf("unexpected first events: %#v", events)
	}
	// The uppercase report is the press and must not refresh. The lowercase
	// report is the release and represents exactly one completed click.
	events = parser.feed([]byte(";7M\x1b[<0;12;7m"))
	if len(events) != 1 || !events[0].click {
		t.Fatalf("expected one click release, got %#v", events)
	}
}

func TestInputParserIgnoresOtherMouseActionsAndKeepsArrows(t *testing.T) {
	var parser inputParser
	events := parser.feed([]byte("\x1b[<2;4;5m\x1b[<64;4;5M\x1b[A"))
	if len(events) != 1 || events[0].key != 'A' || events[0].click {
		t.Fatalf("unexpected events: %#v", events)
	}
}

func TestClickRefreshLimiterDebouncesAndCapsRollingMinute(t *testing.T) {
	var limiter clickRefreshLimiter
	start := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	if !limiter.allow(start) {
		t.Fatal("first click should refresh")
	}
	if limiter.allow(start.Add(100 * time.Millisecond)) {
		t.Fatal("second half of a double-click should not refresh")
	}
	if limiter.allow(start.Add(10 * time.Second)) {
		t.Fatal("second distinct click in one minute should be limited")
	}
	if !limiter.allow(start.Add(61 * time.Second)) {
		t.Fatal("click should refresh after the rolling window advances")
	}
}
