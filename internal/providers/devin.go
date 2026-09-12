package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sobir-git/ai-usage/internal/model"
)

const (
	defaultDevinAPI = "https://server.codeium.com"
	devinService    = "exa.seat_management_pb.SeatManagementService/GetUserStatus"
	devinCompat     = "1.108.2"
)

type DevinCredentials struct {
	APIKey       string
	APIServerURL string
}

func DevinCredentialsPath() string {
	if dataHome := envText("XDG_DATA_HOME"); dataHome != "" {
		return filepath.Join(dataHome, "devin", "credentials.toml")
	}
	return filepath.Join(homeDir(), ".local", "share", "devin", "credentials.toml")
}

func DevinConfigured() bool {
	return envText("DEVIN_API_KEY") != "" || fileExists(DevinCredentialsPath())
}

func loadDevinCredentials() (DevinCredentials, error) {
	path := envText("DEVIN_CREDENTIALS_FILE")
	if path == "" {
		path = DevinCredentialsPath()
	}
	apiKey := envText("DEVIN_API_KEY")
	apiServerURL := envText("DEVIN_API_SERVER_URL")
	if fileExists(path) {
		if apiKey == "" {
			value, err := readTOMLString(path, "windsurf_api_key")
			if err != nil {
				return DevinCredentials{}, usageError("cannot read Devin credentials")
			}
			apiKey = value
		}
		if apiServerURL == "" {
			value, err := readTOMLString(path, "api_server_url")
			if err != nil {
				return DevinCredentials{}, usageError("cannot read Devin credentials")
			}
			apiServerURL = value
		}
	} else if apiKey == "" {
		return DevinCredentials{}, usageError("Devin credentials not found; run `devin auth login` or set DEVIN_API_KEY")
	}
	if apiKey == "" {
		return DevinCredentials{}, usageError("Devin credentials contain no API key; run `devin auth login` again")
	}
	if apiServerURL == "" {
		apiServerURL = defaultDevinAPI
	}
	apiServerURL = strings.TrimRight(apiServerURL, "/")
	if err := validateHTTPSURL(apiServerURL, "Devin API server URL"); err != nil {
		return DevinCredentials{}, err
	}
	return DevinCredentials{APIKey: apiKey, APIServerURL: apiServerURL}, nil
}

func FetchDevin(ctx context.Context, timeout time.Duration, retries int) (*model.DevinUsage, string, error) {
	credentials, err := loadDevinCredentials()
	if err != nil {
		return nil, "", err
	}
	endpoint := joinURL(credentials.APIServerURL, devinService)
	body, err := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"apiKey":           credentials.APIKey,
			"ideName":          "devin",
			"ideVersion":       devinCompat,
			"extensionName":    "devin",
			"extensionVersion": devinCompat,
			"locale":           "en",
		},
	})
	if err != nil {
		return nil, "", usageError("could not prepare Devin usage request")
	}
	response, err := requestJSON(ctx, "POST", endpoint, "Devin", map[string]string{
		"Accept":                   "application/json",
		"Content-Type":             "application/json",
		"Connect-Protocol-Version": "1",
		"User-Agent":               "ai-usage/" + ToolVersion,
	}, body, timeout, retries)
	if err != nil {
		return nil, "", err
	}
	usage, err := normalizeDevinUsage(response, endpoint)
	if err != nil {
		return nil, "", err
	}
	return usage, formatDevin(usage), nil
}

func normalizeDevinUsage(body map[string]any, endpoint string) (*model.DevinUsage, error) {
	userStatus := mapping(pick(body, "userStatus", "user_status"))
	planStatus := mapping(pick(userStatus, "planStatus", "plan_status"))
	planInfo := mapping(pick(planStatus, "planInfo", "plan_info"))
	if len(planInfo) == 0 {
		planInfo = mapping(pick(body, "planInfo", "plan_info"))
	}

	dailyValue, dailyOK := number(pick(planStatus, "dailyQuotaRemainingPercent", "daily_quota_remaining_percent"))
	weeklyValue, weeklyOK := number(pick(planStatus, "weeklyQuotaRemainingPercent", "weekly_quota_remaining_percent"))
	overageMicros, overageOK := number(pick(planStatus, "overageBalanceMicros", "overage_balance_micros"))
	hideDaily, _ := boolValue(pick(planInfo, "hideDailyQuota", "hide_daily_quota"))
	dailyReset := timestampString(pick(planStatus, "dailyQuotaResetAtUnix", "daily_quota_reset_at_unix"))
	dailyInferredZero := !dailyOK && dailyReset != nil && !hideDaily
	if dailyInferredZero {
		dailyValue, dailyOK = 0, true
	}
	var dailyRemaining *float64
	if value, ok := clampPercent(dailyValue, dailyOK); ok {
		dailyRemaining = value
	}
	var weeklyRemaining *float64
	if value, ok := clampPercent(weeklyValue, weeklyOK); ok {
		weeklyRemaining = value
	}

	prompt := nonnegativeNumber(numberValue(pick(planStatus, "availablePromptCredits", "available_prompt_credits")))
	flow := nonnegativeNumber(numberValue(pick(planStatus, "availableFlowCredits", "available_flow_credits")))
	flex := nonnegativeNumber(numberValue(pick(planStatus, "availableFlexCredits", "available_flex_credits")))
	var overage *float64
	if overageOK {
		value := overageMicros
		if value < 0 {
			value = 0
		}
		value /= 1_000_000
		overage = &value
	}
	weeklyReset := timestampString(pick(planStatus, "weeklyQuotaResetAtUnix", "weekly_quota_reset_at_unix"))
	usage := &model.DevinUsage{
		Endpoint:                   endpoint,
		FetchedAt:                  nowUTCString(),
		Plan:                       stringPointer(text(pick(planInfo, "planName", "plan_name"))),
		DailyRemainingPercent:      dailyRemaining,
		DailyUsedPercent:           usedPercent(dailyRemaining),
		DailyRemainingInferredZero: dailyInferredZero,
		DailyResetAt:               dailyReset,
		DailyResetAtLocal:          localTimestamp(dailyReset),
		WeeklyRemainingPercent:     weeklyRemaining,
		WeeklyUsedPercent:          usedPercent(weeklyRemaining),
		WeeklyResetAt:              weeklyReset,
		WeeklyResetAtLocal:         localTimestamp(weeklyReset),
		OverageBalanceDollars:      overage,
		AvailablePromptCredits:     prompt,
		AvailableFlowCredits:       flow,
		AvailableFlexCredits:       flex,
	}
	if dailyRemaining == nil && weeklyRemaining == nil && overage == nil && prompt == nil && flow == nil && flex == nil {
		return nil, usageError("Devin returned no quota or credit fields")
	}
	return usage, nil
}

func formatDevin(usage *model.DevinUsage) string {
	lines := []string{"Plan: " + pointerValue(usage.Plan)}
	if usage.Plan == nil {
		lines[0] = "Plan: unknown"
	}
	if usage.DailyRemainingPercent != nil {
		lines = append(lines, quotaLine("Daily quota", *usage.DailyRemainingPercent, usage.DailyResetAt))
	}
	if usage.WeeklyRemainingPercent != nil {
		lines = append(lines, quotaLine("Weekly quota", *usage.WeeklyRemainingPercent, usage.WeeklyResetAt))
	}
	if usage.OverageBalanceDollars != nil {
		lines = append(lines, fmt.Sprintf("Extra usage balance: $%0.2f", *usage.OverageBalanceDollars))
	}
	if usage.AvailablePromptCredits != nil {
		lines = append(lines, "Prompt credits available: "+numberText(*usage.AvailablePromptCredits))
	}
	if usage.AvailableFlowCredits != nil {
		lines = append(lines, "Flow credits available: "+numberText(*usage.AvailableFlowCredits))
	}
	if usage.AvailableFlexCredits != nil {
		lines = append(lines, "Flex credits available: "+numberText(*usage.AvailableFlexCredits))
	}
	return strings.Join(lines, "\n")
}

func quotaLine(label string, remaining float64, reset *string) string {
	suffix := ""
	if reset != nil {
		if parsed, err := time.Parse(time.RFC3339Nano, *reset); err == nil {
			suffix = " (resets " + formatReset(&parsed, time.Now().UTC()) + ")"
		}
	}
	return fmt.Sprintf("%s: %s%% remaining%s", label, numberText(remaining), suffix)
}

func numberValue(value any) (float64, bool) {
	return number(value)
}

func nonnegativeNumber(value float64, ok bool) *float64 {
	if !ok || value < 0 {
		return nil
	}
	return &value
}
