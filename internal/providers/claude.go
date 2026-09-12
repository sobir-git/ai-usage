package providers

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sobir-git/ai-usage/internal/model"
)

const defaultClaudeUsageURL = "https://api.anthropic.com/api/oauth/usage"

type ClaudeCredentials struct {
	AccessToken      string
	SubscriptionType string
	CredentialsPath  string
}

func ClaudeCredentialsPath() string {
	configDir := envText("CLAUDE_CONFIG_DIR")
	if configDir == "" {
		configDir = filepath.Join(homeDir(), ".claude")
	}
	return filepath.Join(configDir, ".credentials.json")
}

func ClaudeConfigured() bool {
	return envText("CLAUDE_ACCESS_TOKEN") != "" || envText("ANTHROPIC_AUTH_TOKEN") != "" || fileExists(ClaudeCredentialsPath())
}

func loadClaudeCredentials() (ClaudeCredentials, error) {
	path := ClaudeCredentialsPath()
	var values map[string]any
	if fileExists(path) {
		loaded, err := readJSONFile(path)
		if err != nil {
			return ClaudeCredentials{}, usageError("cannot read Claude credentials")
		}
		values = loaded
	} else {
		values = map[string]any{}
	}
	oauth := mapping(values["claudeAiOauth"])
	accessToken := envText("CLAUDE_ACCESS_TOKEN")
	if accessToken == "" {
		accessToken = envText("ANTHROPIC_AUTH_TOKEN")
	}
	if accessToken == "" {
		accessToken = text(oauth["accessToken"])
	}
	if accessToken == "" {
		accessToken = text(oauth["access_token"])
	}
	if accessToken == "" {
		return ClaudeCredentials{}, usageError("Claude credentials not found; run `claude` and log in first")
	}
	return ClaudeCredentials{
		AccessToken:      accessToken,
		SubscriptionType: text(oauth["subscriptionType"]),
		CredentialsPath:  path,
	}, nil
}

func FetchClaude(ctx context.Context, timeout time.Duration, retries int) (*model.ClaudeUsage, string, error) {
	credentials, err := loadClaudeCredentials()
	if err != nil {
		return nil, "", err
	}
	endpoint := envText("CLAUDE_USAGE_URL")
	if endpoint == "" {
		endpoint = defaultClaudeUsageURL
	}
	endpoint = strings.TrimRight(endpoint, "/")
	if err := validateHTTPSURL(endpoint, "Claude usage URL"); err != nil {
		return nil, "", err
	}
	body, err := requestJSON(ctx, "GET", endpoint, "Claude Code", map[string]string{
		"Accept":         "application/json",
		"Authorization":  "Bearer " + credentials.AccessToken,
		"anthropic-beta": "oauth-2025-04-20",
		"User-Agent":     "ai-usage/" + ToolVersion,
	}, nil, timeout, retries)
	if err != nil {
		return nil, "", err
	}
	usage, err := normalizeClaudeUsage(body, credentials, endpoint)
	if err != nil {
		return nil, "", err
	}
	return usage, formatClaude(usage), nil
}

func normalizeClaudeUsage(body map[string]any, credentials ClaudeCredentials, endpoint string) (*model.ClaudeUsage, error) {
	knownWindows := []struct {
		key  string
		name string
	}{
		{"five_hour", "5h"},
		{"seven_day", "7d"},
		{"seven_day_sonnet", "7d Sonnet"},
		{"seven_day_opus", "7d Opus"},
		{"seven_day_cowork", "7d Cowork"},
		{"seven_day_oauth_apps", "7d OAuth apps"},
		{"seven_day_omelette", "7d Omelette"},
	}
	windows := make([]model.Window, 0, len(knownWindows))
	for _, known := range knownWindows {
		value := mapping(body[known.key])
		utilization, ok := number(value["utilization"])
		if !ok {
			continue
		}
		utilization = minMax(utilization, 0, 100)
		reset := isoTimestampString(value["resets_at"])
		windows = append(windows, model.Window{
			Name:             known.name,
			UsedPercent:      utilization,
			RemainingPercent: 100 - utilization,
			ResetAt:          reset,
			ResetAtLocal:     localTimestamp(reset),
		})
	}

	var extraUsage *model.ExtraUsage
	if value := mapping(body["extra_usage"]); len(value) > 0 {
		extraUsage = &model.ExtraUsage{}
		if parsed, ok := boolValue(value["is_enabled"]); ok {
			extraUsage.Enabled = &parsed
		}
		if parsed, ok := number(value["utilization"]); ok {
			extraUsage.Utilization = &parsed
		}
		if parsed, ok := boolValue(value["spend_limit_reached"]); ok {
			extraUsage.SpendLimitReached = &parsed
		}
	}
	if len(windows) == 0 && extraUsage == nil {
		return nil, usageError("Claude Code returned no quota or extra-usage fields")
	}
	plan := credentials.SubscriptionType
	if plan == "" {
		plan = text(body["plan_type"])
	}
	return &model.ClaudeUsage{
		Endpoint:   endpoint,
		FetchedAt:  nowUTCString(),
		Plan:       stringPointer(plan),
		Windows:    windows,
		ExtraUsage: extraUsage,
	}, nil
}

func formatClaude(usage *model.ClaudeUsage) string {
	lines := []string{"Plan: " + humanPlan(pointerValue(usage.Plan))}
	for _, window := range usage.Windows {
		reset := ""
		if window.ResetAt != nil {
			if parsed, err := time.Parse(time.RFC3339Nano, *window.ResetAt); err == nil {
				reset = " (resets " + formatReset(&parsed, time.Now().UTC()) + ")"
			}
		}
		lines = append(lines, fmt.Sprintf("%s: %s%% used (%s%% remaining)%s", window.Name, numberText(window.UsedPercent), numberText(window.RemainingPercent), reset))
	}
	if usage.ExtraUsage != nil {
		if usage.ExtraUsage.Enabled != nil && *usage.ExtraUsage.Enabled {
			lines = append(lines, "Extra usage: enabled")
		} else if usage.ExtraUsage.Enabled != nil {
			lines = append(lines, "Extra usage: disabled")
		} else {
			lines = append(lines, "Extra usage: unknown")
		}
	}
	return strings.Join(lines, "\n")
}
