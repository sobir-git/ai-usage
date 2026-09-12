package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sobir-git/ai-usage/internal/model"
	"github.com/sobir-git/ai-usage/internal/providers"
)

type ProviderSpec struct {
	Key        string
	ID         string
	Name       string
	Configured func() bool
	Fetch      func(context.Context, time.Duration, int) (any, string, error)
}

type ProviderResult struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Usage    any    `json:"usage,omitempty"`
	Error    string `json:"error,omitempty"`
	Rendered string `json:"-"`
}

type Snapshot struct {
	SchemaVersion int              `json:"schema_version"`
	FetchedAt     string           `json:"fetched_at"`
	Timezone      string           `json:"timezone"`
	Providers     []ProviderResult `json:"providers"`
}

func ProviderSpecs() []ProviderSpec {
	return []ProviderSpec{
		{Key: "devin", ID: "devin", Name: "Devin", Configured: providers.DevinConfigured, Fetch: fetchDevin},
		{Key: "codex", ID: "codex", Name: "Codex", Configured: func() bool { return len(providers.DiscoverCodexHomes("")) > 0 }, Fetch: fetchCodex},
		{Key: "claude", ID: "claude_code", Name: "Claude Code", Configured: providers.ClaudeConfigured, Fetch: fetchClaude},
	}
}

func SelectProviders(keys []string) ([]ProviderSpec, error) {
	specs := ProviderSpecs()
	byKey := make(map[string]ProviderSpec, len(specs))
	for _, spec := range specs {
		byKey[spec.Key] = spec
	}
	if len(keys) == 0 {
		return specs, nil
	}
	selected := make([]ProviderSpec, 0, len(keys))
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		if seen[key] {
			continue
		}
		spec, ok := byKey[key]
		if !ok {
			return nil, fmt.Errorf("unknown provider %q", key)
		}
		selected = append(selected, spec)
		seen[key] = true
	}
	return selected, nil
}

func Collect(ctx context.Context, selected []ProviderSpec, timeout time.Duration, retries int) []ProviderResult {
	results := make([]ProviderResult, len(selected))
	var waitGroup sync.WaitGroup
	for index, spec := range selected {
		results[index] = ProviderResult{ID: spec.ID, Name: spec.Name, Status: "not_configured"}
		configured := false
		func() {
			defer func() { _ = recover() }()
			configured = spec.Configured()
		}()
		if !configured {
			continue
		}
		results[index].Status = "error"
		waitGroup.Add(1)
		go func(index int, spec ProviderSpec) {
			defer waitGroup.Done()
			usage, rendered, err := spec.Fetch(ctx, timeout, retries)
			if err != nil {
				results[index].Error = safeError(err.Error())
				return
			}
			results[index].Usage = usage
			results[index].Rendered = rendered
			results[index].Status = "ok"
			if aggregate, ok := usage.(*model.CodexAggregate); ok {
				for _, account := range aggregate.Accounts {
					if account.Status != "ok" {
						results[index].Status = "partial"
						break
					}
				}
			}
		}(index, spec)
	}
	waitGroup.Wait()
	return results
}

func FormatHuman(results []ProviderResult, showMissing bool) string {
	zone, _ := time.Now().Zone()
	if zone == "" {
		zone = "local time"
	}
	lines := []string{"AI usage (local time: " + zone + ")"}
	visible := make([]ProviderResult, 0, len(results))
	for _, result := range results {
		if result.Status != "not_configured" || showMissing {
			visible = append(visible, result)
		}
	}
	if len(visible) == 0 {
		return "No configured providers found."
	}
	for _, result := range visible {
		lines = append(lines, "", result.Name)
		switch {
		case (result.Status == "ok" || result.Status == "partial") && result.Rendered != "":
			for _, line := range strings.Split(result.Rendered, "\n") {
				lines = append(lines, "  "+line)
			}
		case result.Status == "not_configured":
			lines = append(lines, "  Not configured")
		default:
			errorText := result.Error
			if errorText == "" {
				errorText = "unknown error"
			}
			lines = append(lines, "  Error: "+errorText)
		}
	}
	return strings.Join(lines, "\n")
}

func MakeSnapshot(results []ProviderResult) Snapshot {
	zone, _ := time.Now().Zone()
	if zone == "" {
		zone = "local time"
	}
	return Snapshot{
		SchemaVersion: 1,
		FetchedAt:     time.Now().Format(time.RFC3339Nano),
		Timezone:      zone,
		Providers:     results,
	}
}

func ExitCode(results []ProviderResult, strict bool) int {
	configured := false
	hasSuccess := false
	hasError := false
	for _, result := range results {
		if result.Status == "not_configured" {
			continue
		}
		configured = true
		if result.Status == "ok" || result.Status == "partial" {
			hasSuccess = true
		}
		if result.Status == "error" || result.Status == "partial" {
			hasError = true
		}
	}
	if !configured || (strict && hasError) || (configured && !hasSuccess) {
		return 1
	}
	return 0
}

func fetchDevin(ctx context.Context, timeout time.Duration, retries int) (any, string, error) {
	return providers.FetchDevin(ctx, timeout, retries)
}

func fetchCodex(ctx context.Context, timeout time.Duration, retries int) (any, string, error) {
	return providers.FetchCodex(ctx, timeout, retries)
}

func fetchClaude(ctx context.Context, timeout time.Duration, retries int) (any, string, error) {
	return providers.FetchClaude(ctx, timeout, retries)
}

func safeError(value string) string {
	var builder strings.Builder
	space := false
	for _, runeValue := range value {
		if runeValue < 32 || runeValue == 127 || runeValue == '\n' || runeValue == '\r' || runeValue == '\t' {
			if !space {
				builder.WriteByte(' ')
				space = true
			}
			continue
		}
		builder.WriteRune(runeValue)
		space = false
		if builder.Len() >= 180 {
			break
		}
	}
	return strings.TrimSpace(builder.String())
}
