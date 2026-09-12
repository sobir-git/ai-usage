package providers

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiscoverCodexHomesIsShallowAndSkipsSymlinks(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{".codex", ".codex-2"} {
		profile := filepath.Join(root, name)
		if err := os.Mkdir(profile, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(profile, "auth.json"), []byte(`{"tokens":{"access_token":"test"}}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	nested := filepath.Join(root, "project", ".codex-3")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "auth.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, ".codex"), filepath.Join(root, ".codex-link")); err != nil {
		t.Fatal(err)
	}

	homes := DiscoverCodexHomes(root)
	if len(homes) != 2 || codexProfileLabel(homes[0]) != "default" || codexProfileLabel(homes[1]) != "codex-2" {
		t.Fatalf("unexpected homes: %#v", homes)
	}
}

func TestUsageURLForBase(t *testing.T) {
	if got := usageURLForBase("https://chatgpt.com/backend-api"); got != "https://chatgpt.com/backend-api/wham/usage" {
		t.Fatalf("unexpected ChatGPT endpoint: %s", got)
	}
	if got := usageURLForBase("https://example.test"); got != "https://example.test/api/codex/usage" {
		t.Fatalf("unexpected custom endpoint: %s", got)
	}
}

func TestNormalizeCodexUsageKeepsModelWindowsInJSON(t *testing.T) {
	usage, err := normalizeCodexUsage(map[string]any{
		"plan_type": "pro",
		"rate_limit": map[string]any{
			"primary_window": map[string]any{
				"used_percent":         96,
				"limit_window_seconds": 604800,
				"reset_at":             1789286400,
			},
		},
		"additional_rate_limits": []any{
			map[string]any{
				"limit_name": "GPT-5.3-Codex-Spark",
				"rate_limit": map[string]any{
					"primary_window": map[string]any{"used_percent": 0, "limit_window_seconds": 18000},
				},
			},
		},
	}, "https://example.test/api/codex/usage")
	if err != nil {
		t.Fatal(err)
	}
	if len(usage.Windows) != 2 || usage.Windows[1].Name != "GPT-5.3-Codex-Spark (5h)" {
		t.Fatalf("unexpected windows: %#v", usage.Windows)
	}
	if rendered := formatCodex(usage); strings.Contains(rendered, "Spark") {
		t.Fatalf("model-specific window leaked into human output: %s", rendered)
	}
}

func TestNormalizeDevinInfersDailyZeroOnlyWithReset(t *testing.T) {
	usage, err := normalizeDevinUsage(map[string]any{
		"userStatus": map[string]any{
			"planStatus": map[string]any{
				"dailyQuotaResetAtUnix": 1789214400,
				"planInfo":              map[string]any{"planName": "Pro"},
			},
		},
	}, "https://example.test/status")
	if err != nil {
		t.Fatal(err)
	}
	if usage.DailyRemainingPercent == nil || *usage.DailyRemainingPercent != 0 || !usage.DailyRemainingInferredZero {
		t.Fatalf("daily quota was not inferred as zero: %#v", usage)
	}
}

func TestFormatResetUsesCountdownUnderTwelveHours(t *testing.T) {
	now := time.Date(2026, 9, 12, 6, 0, 0, 0, time.UTC)
	reset := now.Add(2*time.Hour + 14*time.Minute)
	if got := formatReset(&reset, now); got != "in 2h 14m" {
		t.Fatalf("unexpected countdown: %s", got)
	}
}

func TestHTTPClientDoesNotFollowRedirects(t *testing.T) {
	if err := newHTTPClient().CheckRedirect(nil, nil); err != http.ErrUseLastResponse {
		t.Fatalf("unexpected redirect policy: %v", err)
	}
}
