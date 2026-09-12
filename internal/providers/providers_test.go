package providers

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFallbackDoesNotPromoteSparkToOverall(t *testing.T) {
	window := func(used int) map[string]any {
		return map[string]any{"primary": map[string]any{"usedPercent": used, "windowDurationMins": 300}}
	}
	for i := 0; i < 20; i++ {
		payload := appServerPayload(map[string]any{"rateLimitsByLimitId": map[string]any{"codex": window(80), "codex_spark": window(0)}})
		usage, err := normalizeCodexUsage(payload, "app-server")
		if err != nil || len(usage.Windows) != 2 || usage.Windows[0].UsedPercent != 80 {
			t.Fatalf("wrong main quota: %+v, %v", usage, err)
		}
	}
	if _, err := normalizeCodexUsage(appServerPayload(map[string]any{}), "app-server"); err == nil {
		t.Fatal("empty fallback fabricated credits")
	}
}

func TestCustomDevinCredentialsDetected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.toml")
	if err := os.WriteFile(path, []byte("windsurf_api_key = 'test' # comment\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVIN_CREDENTIALS_FILE", path)
	t.Setenv("DEVIN_API_KEY", "")
	if !DevinConfigured() {
		t.Fatal("custom credentials ignored")
	}
	credentials, err := loadDevinCredentials()
	if err != nil || credentials.APIKey != "test" {
		t.Fatalf("credentials parsing failed: %v", err)
	}
}

func TestTOMLRootAndInlineComment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("chatgpt_base_url = \"https://example.test\" # hello\n[other]\nother_key = 'wrong'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := readTOMLString(path, "chatgpt_base_url")
	if err != nil || got != "https://example.test" {
		t.Fatalf("got %q, %v", got, err)
	}
	got, err = readTOMLString(path, "other_key")
	if err != nil || got != "" {
		t.Fatal("read nested key as root setting")
	}
}

func TestRateLimitDoesNotRapidlyRetry(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(429) }))
	defer server.Close()
	_, err := requestJSON(context.Background(), "GET", server.URL, "test", nil, nil, time.Second, 2)
	if err == nil || calls != 1 || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("calls %d, error %v", calls, err)
	}
}

func TestRPCResponseSizeBound(t *testing.T) {
	_, err := readRPCResponse(context.Background(), bufio.NewReader(strings.NewReader(strings.Repeat("x", MaxResponseBytes+1))), 1)
	if err == nil || !strings.Contains(err.Error(), "oversized") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTimestampBeyondNanosecondRange(t *testing.T) {
	got, ok := timestamp(16725225600.0) // 2500-01-01
	if !ok || got.Year() != 2500 {
		t.Fatalf("timestamp overflow: %v", got)
	}
}

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
