package app

import (
	"testing"

	"github.com/sobir-git/ai-usage/internal/model"
)

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
