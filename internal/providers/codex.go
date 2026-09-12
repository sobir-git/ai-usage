package providers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sobir-git/ai-usage/internal/model"
)

const (
	defaultCodexBaseURL = "https://chatgpt.com/backend-api"
)

type CodexCredentials struct {
	AccessToken string
	AccountID   string
	BaseURL     string
}

// DiscoverCodexHomes intentionally only inspects direct entries in the home
// directory. It never walks project trees and skips symlinked profiles/files.
func DiscoverCodexHomes(root string) []string {
	if root == "" {
		root = homeDir()
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var homes []string
	for _, entry := range entries {
		name := entry.Name()
		if name != ".codex" && !strings.HasPrefix(name, ".codex-") {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			continue
		}
		profile := filepath.Join(root, name)
		info, err := os.Lstat(filepath.Join(profile, "auth.json"))
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			continue
		}
		homes = append(homes, profile)
	}
	sort.Slice(homes, func(i, j int) bool {
		left, right := codexProfileLabel(homes[i]), codexProfileLabel(homes[j])
		if left == "default" {
			return true
		}
		if right == "default" {
			return false
		}
		return left < right
	})
	return homes
}

func codexProfileLabel(home string) string {
	name := filepath.Base(filepath.Clean(home))
	if name == ".codex" {
		return "default"
	}
	return strings.TrimPrefix(name, ".")
}

func loadCodexCredentials(codexHome string) (CodexCredentials, error) {
	authPath := filepath.Join(codexHome, "auth.json")
	values, err := readJSONFile(authPath)
	if err != nil {
		if isNotExist(err) {
			return CodexCredentials{}, usageError("Codex credentials not found; run `codex login` first")
		}
		return CodexCredentials{}, usageError("cannot read Codex credentials")
	}
	tokens := mapping(values["tokens"])
	accessToken := text(tokens["access_token"])
	if accessToken == "" {
		accessToken = text(values["access_token"])
	}
	if accessToken == "" {
		return CodexCredentials{}, usageError("Codex credentials contain no access token; run `codex login` again")
	}
	accountID := text(tokens["account_id"])
	if accountID == "" {
		accountID = text(values["account_id"])
	}
	baseURL := envText("CODEX_CHATGPT_BASE_URL")
	if baseURL == "" {
		if configured, readErr := readTOMLString(filepath.Join(codexHome, "config.toml"), "chatgpt_base_url"); readErr == nil {
			baseURL = configured
		}
	}
	if baseURL == "" {
		baseURL = defaultCodexBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if err := validateHTTPSURL(baseURL, "Codex base URL"); err != nil {
		return CodexCredentials{}, err
	}
	return CodexCredentials{AccessToken: accessToken, AccountID: accountID, BaseURL: baseURL}, nil
}

func usageURLForBase(baseURL string) string {
	baseURL = strings.TrimRight(baseURL, "/")
	if strings.Contains(baseURL, "/backend-api") {
		return baseURL + "/wham/usage"
	}
	return baseURL + "/api/codex/usage"
}

func FetchCodex(ctx context.Context, timeout time.Duration, retries int) (*model.CodexAggregate, string, error) {
	homes := DiscoverCodexHomes("")
	if len(homes) == 0 {
		return nil, "", usageError("no Codex profiles found directly in the home directory")
	}
	type result struct {
		account model.CodexAccount
		render  string
	}
	results := make(chan result, len(homes))
	for _, home := range homes {
		home := home
		go func() {
			profile := codexProfileLabel(home)
			usage, rendered, err := fetchCodexProfile(ctx, home, timeout, retries)
			if err != nil {
				errText := safeError(err.Error())
				results <- result{
					account: model.CodexAccount{Profile: profile, Status: "error", Error: errText},
					render:  fmt.Sprintf("Profile: %s\n  Error: %s", profile, errText),
				}
				return
			}
			results <- result{
				account: model.CodexAccount{Profile: profile, Status: "ok", Usage: usage},
				render:  renderCodexProfile(profile, rendered),
			}
		}()
	}

	accounts := make([]model.CodexAccount, 0, len(homes))
	renders := make(map[string]string, len(homes))
	for range homes {
		value := <-results
		accounts = append(accounts, value.account)
		renders[value.account.Profile] = value.render
	}
	sort.Slice(accounts, func(i, j int) bool {
		if accounts[i].Profile == "default" {
			return true
		}
		if accounts[j].Profile == "default" {
			return false
		}
		return accounts[i].Profile < accounts[j].Profile
	})
	rendered := make([]string, 0, len(accounts))
	successes := 0
	for _, account := range accounts {
		rendered = append(rendered, renders[account.Profile])
		if account.Status == "ok" {
			successes++
		}
	}
	if successes == 0 {
		var errorsText []string
		for _, account := range accounts {
			errorsText = append(errorsText, account.Profile+": "+account.Error)
		}
		return nil, "", usageError("all Codex profiles failed: %s", strings.Join(errorsText, "; "))
	}
	return &model.CodexAggregate{Accounts: accounts}, strings.Join(rendered, "\n\n"), nil
}

func fetchCodexProfile(ctx context.Context, codexHome string, timeout time.Duration, retries int) (*model.CodexUsage, string, error) {
	credentials, err := loadCodexCredentials(codexHome)
	if err != nil {
		return nil, "", err
	}
	endpoint := usageURLForBase(credentials.BaseURL)
	headers := map[string]string{
		"Accept":        "application/json",
		"Authorization": "Bearer " + credentials.AccessToken,
		"User-Agent":    "ai-usage/" + ToolVersion,
	}
	if credentials.AccountID != "" {
		headers["ChatGPT-Account-Id"] = credentials.AccountID
	}
	body, requestErr := requestJSON(ctx, "GET", endpoint, "Codex", headers, nil, timeout, retries)
	if requestErr != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		fallbackEndpoint, fallbackBody, fallbackErr := fetchAppServerUsage(ctx, codexHome, timeout)
		if fallbackErr != nil {
			return nil, "", usageError("live usage request failed: %s; app-server fallback failed: %s", safeError(requestErr.Error()), safeError(fallbackErr.Error()))
		}
		endpoint, body = fallbackEndpoint, fallbackBody
	}
	usage, err := normalizeCodexUsage(body, endpoint)
	if err != nil {
		return nil, "", err
	}
	return usage, formatCodex(usage), nil
}

func normalizeCodexUsage(body map[string]any, endpoint string) (*model.CodexUsage, error) {
	windows := make([]model.Window, 0, 4)
	appendCodexWindows(&windows, "Overall", mapping(body["rate_limit"]))
	if additional, ok := body["additional_rate_limits"].([]any); ok {
		for _, item := range additional {
			itemMapping := mapping(item)
			name := text(itemMapping["limit_name"])
			if name == "" {
				name = text(itemMapping["metered_feature"])
			}
			if name == "" {
				name = "Additional"
			}
			appendCodexWindows(&windows, name, mapping(itemMapping["rate_limit"]))
		}
	}

	var credits *model.Credits
	if value := mapping(body["credits"]); len(value) > 0 {
		credits = &model.Credits{Balance: numberOrText(value["balance"])}
		if parsed, ok := boolValue(value["has_credits"]); ok {
			credits.HasCredits = &parsed
		}
		if parsed, ok := boolValue(value["unlimited"]); ok {
			credits.Unlimited = &parsed
		}
	}
	if len(windows) == 0 && credits == nil {
		return nil, usageError("Codex returned no quota or credit fields")
	}
	return &model.CodexUsage{
		Endpoint:  endpoint,
		FetchedAt: nowUTCString(),
		Plan:      stringPointer(text(body["plan_type"])),
		Windows:   windows,
		Credits:   credits,
	}, nil
}

func appendCodexWindows(windows *[]model.Window, scope string, rateLimit map[string]any) {
	for _, slot := range []string{"primary_window", "secondary_window"} {
		window := mapping(rateLimit[slot])
		used, ok := number(window["used_percent"])
		if !ok {
			continue
		}
		used = minMax(used, 0, 100)
		remaining := 100 - used
		var seconds *int64
		if duration, ok := number(window["limit_window_seconds"]); ok && duration > 0 {
			value := int64(duration)
			seconds = &value
		}
		reset := timestampString(window["reset_at"])
		name := durationName(seconds)
		if name == "" {
			name = scope
		}
		if scope != "Overall" {
			name = scope + " (" + name + ")"
		} else if slot == "secondary_window" && durationName(seconds) != "" {
			name += " secondary"
		}
		*windows = append(*windows, model.Window{
			Name:             name,
			UsedPercent:      used,
			RemainingPercent: remaining,
			WindowSeconds:    seconds,
			ResetAt:          reset,
			ResetAtLocal:     localTimestamp(reset),
		})
	}
}

func durationName(seconds *int64) string {
	if seconds == nil {
		return ""
	}
	value := *seconds
	if value%86400 == 0 {
		return fmt.Sprintf("%dd", value/86400)
	}
	if value%3600 == 0 {
		return fmt.Sprintf("%dh", value/3600)
	}
	if value%60 == 0 {
		return fmt.Sprintf("%dm", value/60)
	}
	return fmt.Sprintf("%ds", value)
}

func formatCodex(usage *model.CodexUsage) string {
	lines := []string{"Plan: " + humanPlan(pointerValue(usage.Plan))}
	for _, window := range usage.Windows {
		if strings.Contains(window.Name, " (") {
			continue
		}
		reset := ""
		if window.ResetAt != nil {
			if parsed, err := time.Parse(time.RFC3339Nano, *window.ResetAt); err == nil {
				reset = " (resets " + formatReset(&parsed, time.Now().UTC()) + ")"
			}
		}
		lines = append(lines, fmt.Sprintf("%s: %s%% used (%s%% remaining)%s", window.Name, numberText(window.UsedPercent), numberText(window.RemainingPercent), reset))
	}
	if usage.Credits != nil {
		if usage.Credits.Unlimited != nil && *usage.Credits.Unlimited {
			lines = append(lines, "Credits: unlimited")
		} else if usage.Credits.HasCredits != nil && *usage.Credits.HasCredits {
			suffix := ""
			if usage.Credits.Balance != nil {
				suffix = fmt.Sprintf(" (%v)", usage.Credits.Balance)
			}
			lines = append(lines, "Credits: available"+suffix)
		} else {
			lines = append(lines, "Credits: none")
		}
	}
	return strings.Join(lines, "\n")
}

func renderCodexProfile(profile, content string) string {
	lines := []string{"Profile: " + profile}
	for _, line := range strings.Split(content, "\n") {
		lines = append(lines, "  "+line)
	}
	return strings.Join(lines, "\n")
}

func fetchAppServerUsage(parent context.Context, codexHome string, timeout time.Duration) (string, map[string]any, error) {
	executable, err := exec.LookPath("codex")
	if err != nil {
		return "", nil, usageError("Codex executable was not found for app-server fallback")
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-s", "read-only", "-a", "on-request", "app-server")
	command.Env = append(os.Environ(), "CODEX_HOME="+codexHome)
	command.Stderr = io.Discard
	stdin, err := command.StdinPipe()
	if err != nil {
		return "", nil, usageError("could not prepare Codex app-server")
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		stdin.Close()
		return "", nil, usageError("could not read Codex app-server")
	}
	if err := command.Start(); err != nil {
		stdin.Close()
		return "", nil, usageError("could not start Codex app-server")
	}
	reader := bufio.NewReader(stdout)
	defer func() {
		stdin.Close()
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		_ = command.Wait()
	}()

	if err := writeRPC(stdin, map[string]any{
		"id":     1,
		"method": "initialize",
		"params": map[string]any{"clientInfo": map[string]any{"name": "ai-usage", "version": ToolVersion}},
	}); err != nil {
		return "", nil, usageError("Codex app-server initialization failed")
	}
	if _, err := readRPCResponse(ctx, reader, 1); err != nil {
		if ctx.Err() != nil {
			return "", nil, usageError("Codex app-server timed out")
		}
		return "", nil, err
	}
	if err := writeRPC(stdin, map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		return "", nil, usageError("Codex app-server initialization failed")
	}
	if err := writeRPC(stdin, map[string]any{"id": 2, "method": "account/rateLimits/read", "params": map[string]any{}}); err != nil {
		return "", nil, usageError("Codex app-server rate-limit request failed")
	}
	response, err := readRPCResponse(ctx, reader, 2)
	if err != nil {
		if ctx.Err() != nil {
			return "", nil, usageError("Codex app-server timed out")
		}
		return "", nil, err
	}
	if response["error"] != nil {
		return "", nil, usageError("Codex app-server rejected the rate-limit request")
	}
	result := mapping(response["result"])
	if len(result) == 0 {
		return "", nil, usageError("Codex app-server returned an empty rate-limit response")
	}
	return "codex app-server account/rateLimits/read", appServerPayload(result), nil
}

func writeRPC(writer io.Writer, message map[string]any) error {
	body, err := json.Marshal(message)
	if err != nil {
		return err
	}
	body = append(body, '\n')
	_, err = writer.Write(body)
	return err
}

func readRPCResponse(parent context.Context, reader *bufio.Reader, responseID int) (map[string]any, error) {
	type result struct {
		value map[string]any
		err   error
	}
	readDone := make(chan result, 1)
	go func() {
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				readDone <- result{err: usageError("Codex app-server closed before returning rate limits")}
				return
			}
			var value map[string]any
			decoder := json.NewDecoder(strings.NewReader(string(line)))
			decoder.UseNumber()
			if decoder.Decode(&value) != nil {
				continue
			}
			if parsed, ok := number(value["id"]); ok && int(parsed) == responseID {
				readDone <- result{value: value}
				return
			}
		}
	}()
	select {
	case <-parent.Done():
		return nil, parent.Err()
	case value := <-readDone:
		return value.value, value.err
	}
}

func appServerPayload(result map[string]any) map[string]any {
	rateLimits := mapping(result["rateLimits"])
	byLimitID := mapping(result["rateLimitsByLimitId"])
	if len(rateLimits) == 0 && len(byLimitID) > 0 {
		for _, value := range byLimitID {
			rateLimits = mapping(value)
			break
		}
	}
	payload := map[string]any{
		"plan_type":  text(rateLimits["planType"]),
		"rate_limit": appServerRateLimit(rateLimits),
		"credits":    appServerCredits(mapping(rateLimits["credits"])),
	}
	mainLimitID := text(rateLimits["limitId"])
	additional := make([]any, 0)
	for limitID, value := range byLimitID {
		if limitID == mainLimitID {
			continue
		}
		limit := mapping(value)
		additional = append(additional, map[string]any{
			"metered_feature": limitID,
			"limit_name":      text(limit["limitName"]),
			"rate_limit":      appServerRateLimit(limit),
		})
	}
	if len(additional) > 0 {
		payload["additional_rate_limits"] = additional
	}
	return payload
}

func appServerRateLimit(value map[string]any) map[string]any {
	return map[string]any{
		"primary_window":   appServerWindow(value["primary"]),
		"secondary_window": appServerWindow(value["secondary"]),
	}
}

func appServerWindow(value any) map[string]any {
	window := mapping(value)
	used, ok := number(window["usedPercent"])
	if !ok {
		return nil
	}
	minutes, _ := number(window["windowDurationMins"])
	var seconds any
	if minutes > 0 {
		seconds = int64(minutes * 60)
	}
	return map[string]any{
		"used_percent":         used,
		"limit_window_seconds": seconds,
		"reset_at":             window["resetsAt"],
	}
}

func appServerCredits(value map[string]any) map[string]any {
	return map[string]any{
		"balance":     numberOrText(value["balance"]),
		"has_credits": boolOrNil(value["hasCredits"]),
		"unlimited":   boolOrNil(value["unlimited"]),
	}
}

func boolOrNil(value any) any {
	if parsed, ok := boolValue(value); ok {
		return parsed
	}
	return nil
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func pointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func minMax(value, minimum, maximum float64) float64 {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}
