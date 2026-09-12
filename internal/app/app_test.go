package app

import (
	"context"
	"testing"
	"time"

	"github.com/sobir-git/ai-usage/internal/model"
)

func TestAllCodexFailuresRemainStructured(t *testing.T) {
	spec := ProviderSpec{ID: "codex", Name: "Codex", Configured: func() bool { return true }, Fetch: func(context.Context, time.Duration, int) (any, string, error) {
		return &model.CodexAggregate{Accounts: []model.CodexAccount{{Profile: "default", Status: "error", Error: "expired"}, {Profile: "codex-2", Status: "error", Error: "expired"}}}, "default: expired\ncodex-2: expired", nil
	}}
	results := Collect(context.Background(), []ProviderSpec{spec}, time.Second, 0)
	if results[0].Status != "error" || results[0].Usage == nil || ExitCode(results, false) != 1 {
		t.Fatalf("incorrect failure handling: %+v", results)
	}
}

func TestPartialCodexResultIsSuccessfulButStrict(t *testing.T) {
	results := []ProviderResult{{
		ID:     "codex",
		Name:   "Codex",
		Status: "partial",
		Usage: &model.CodexAggregate{Accounts: []model.CodexAccount{
			{Profile: "default", Status: "ok"},
			{Profile: "codex-2", Status: "error", Error: "expired login"},
		}},
		Rendered: "Profile: default\n  Plan: Pro\n\nProfile: codex-2\n  Error: expired login",
	}}
	if got := ExitCode(results, false); got != 0 {
		t.Fatalf("partial non-strict result should succeed, got %d", got)
	}
	if got := ExitCode(results, true); got != 1 {
		t.Fatalf("partial strict result should fail, got %d", got)
	}
	if output := FormatHuman(results, false); output == "" {
		t.Fatal("expected human output")
	}
}
