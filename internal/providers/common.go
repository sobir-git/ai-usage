package providers

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	ToolVersion      = "0.2.0"
	MaxResponseBytes = 1 << 20
)

var retryableStatuses = map[int]bool{
	408: true,
	425: true,
	429: true,
	500: true,
	502: true,
	503: true,
	504: true,
}

type UsageError struct {
	Message string
}

func (e *UsageError) Error() string { return e.Message }

func usageError(format string, args ...any) error {
	return &UsageError{Message: fmt.Sprintf(format, args...)}
}

func homeDir() string {
	if value, err := os.UserHomeDir(); err == nil && value != "" {
		return value
	}
	return os.Getenv("HOME")
}

func envText(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}

func text(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func mapping(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return map[string]any{}
}

func pick(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value
		}
	}
	return nil
}

func boolValue(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1":
			return true, true
		case "false", "0":
			return false, true
		}
	}
	return false, false
}

func number(value any) (float64, bool) {
	var parsed float64
	switch typed := value.(type) {
	case float64:
		parsed = typed
	case float32:
		parsed = float64(typed)
	case int:
		parsed = float64(typed)
	case int64:
		parsed = float64(typed)
	case uint64:
		parsed = float64(typed)
	case json.Number:
		value, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		parsed = value
	case string:
		value, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, false
		}
		parsed = value
	default:
		return 0, false
	}
	if math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0, false
	}
	return parsed, true
}

func numberOrText(value any) any {
	if parsed, ok := number(value); ok {
		if parsed == math.Trunc(parsed) && parsed >= math.MinInt64 && parsed <= math.MaxInt64 {
			return int64(parsed)
		}
		return parsed
	}
	if value := text(value); value != "" {
		return value
	}
	return nil
}

func clampPercent(value float64, ok bool) (*float64, bool) {
	if !ok {
		return nil, false
	}
	value = math.Min(100, math.Max(0, value))
	return &value, true
}

func usedPercent(remaining *float64) *float64 {
	if remaining == nil {
		return nil
	}
	value := 100 - *remaining
	return &value
}

func timestamp(value any) (*time.Time, bool) {
	if seconds, ok := number(value); ok {
		if seconds <= 0 {
			return nil, false
		}
		if seconds > 100_000_000_000 {
			seconds /= 1000
		}
		if seconds > 253402300799 {
			return nil, false
		}
		whole, fraction := math.Modf(seconds)
		value := time.Unix(int64(whole), int64(fraction*float64(time.Second))).UTC()
		return &value, true
	}
	return nil, false
}

func isoTimestamp(value any) (*time.Time, bool) {
	if parsed, ok := timestamp(value); ok {
		return parsed, true
	}
	valueText := text(value)
	if valueText == "" {
		return nil, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, strings.Replace(valueText, "Z", "+00:00", 1))
	if err != nil {
		return nil, false
	}
	parsed = parsed.UTC()
	return &parsed, true
}

func timestampString(value any) *string {
	parsed, ok := timestamp(value)
	if !ok {
		return nil
	}
	formatted := parsed.Format(time.RFC3339Nano)
	return &formatted
}

func isoTimestampString(value any) *string {
	parsed, ok := isoTimestamp(value)
	if !ok {
		return nil
	}
	formatted := parsed.Format(time.RFC3339Nano)
	return &formatted
}

func localTimestamp(value *string) *string {
	if value == nil {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, *value)
	if err != nil {
		return nil
	}
	formatted := parsed.In(time.Local).Format(time.RFC3339Nano)
	return &formatted
}

func nowUTCString() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func formatReset(resetAt *time.Time, now time.Time) string {
	if resetAt == nil {
		return "unknown"
	}
	delta := resetAt.Sub(now)
	if delta > 0 && delta < 12*time.Hour {
		minutes := int(math.Ceil(delta.Minutes()))
		if minutes < 1 {
			minutes = 1
		}
		hours := minutes / 60
		minutes %= 60
		if hours > 0 && minutes > 0 {
			return fmt.Sprintf("in %dh %dm", hours, minutes)
		}
		if hours > 0 {
			return fmt.Sprintf("in %dh", hours)
		}
		return fmt.Sprintf("in %dm", minutes)
	}
	if delta <= 0 {
		return "now"
	}
	return resetAt.In(time.Local).Format("Jan 2 at 3:04 PM MST")
}

func numberText(value float64) string {
	if value == math.Trunc(value) {
		return strconv.FormatInt(int64(value), 10)
	}
	return strconv.FormatFloat(value, 'g', -1, 64)
}

func humanPlan(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	words := strings.Fields(strings.ReplaceAll(value, "_", " "))
	for i, word := range words {
		if word == "" {
			continue
		}
		words[i] = strings.ToUpper(word[:1]) + strings.ToLower(word[1:])
	}
	return strings.Join(words, " ")
}

func retryDelay(attempt int) time.Duration {
	delay := 250 * time.Millisecond
	for i := 0; i < attempt; i++ {
		delay *= 2
		if delay >= 2*time.Second {
			return 2 * time.Second
		}
	}
	return delay
}

func waitRetry(ctx context.Context, attempt int) error {
	timer := time.NewTimer(retryDelay(attempt))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func validateHTTPSURL(value, label string) error {
	parsed, err := url.Parse(value)
	if err != nil || strings.ToLower(parsed.Scheme) != "https" || parsed.Host == "" || parsed.User != nil {
		return usageError("%s must be an HTTPS URL", label)
	}
	return nil
}

func newHTTPClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func requestJSON(ctx context.Context, method, endpoint, label string, headers map[string]string, body []byte, timeout time.Duration, retries int) (map[string]any, error) {
	client := newHTTPClient()
	for attempt := 0; attempt <= retries; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		attemptCtx, cancel := context.WithTimeout(ctx, timeout)
		request, err := http.NewRequestWithContext(attemptCtx, method, endpoint, strings.NewReader(string(body)))
		if err != nil {
			cancel()
			return nil, usageError("%s request could not be created", label)
		}
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		response, err := client.Do(request)
		if err != nil {
			cancel()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if attempt < retries {
				if err := waitRetry(ctx, attempt); err != nil {
					return nil, err
				}
				continue
			}
			return nil, usageError("request to %s failed: %s", label, safeError(err.Error()))
		}
		responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, MaxResponseBytes+1))
		response.Body.Close()
		cancel()
		if readErr != nil {
			if attempt < retries {
				if err := waitRetry(ctx, attempt); err != nil {
					return nil, err
				}
				continue
			}
			return nil, usageError("reading %s response failed", label)
		}
		if len(responseBody) > MaxResponseBytes {
			return nil, usageError("%s returned an oversized response", label)
		}
		// Repeated immediate retries make a quota-service rate limit worse.
		// Let the user retry explicitly after the provider's cooldown.
		if response.StatusCode == http.StatusTooManyRequests {
			return nil, usageError("%s is rate limited (HTTP 429); try again later", label)
		}
		if retryableStatuses[response.StatusCode] && attempt < retries {
			if err := waitRetry(ctx, attempt); err != nil {
				return nil, err
			}
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, httpError(label, response.StatusCode, responseBody)
		}
		return decodeObject(label, responseBody)
	}
	return nil, usageError("request to %s failed after all retries", label)
}

func decodeObject(label string, body []byte) (map[string]any, error) {
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, usageError("%s returned a non-JSON usage response", label)
	}
	parsed, ok := value.(map[string]any)
	if !ok {
		return nil, usageError("%s returned an unexpected usage response", label)
	}
	return parsed, nil
}

func httpError(label string, status int, body []byte) error {
	detail := ""
	if parsed, err := decodeObjectForError(body); err == nil {
		detail = text(pick(parsed, "message", "error", "code"))
	}
	if detail != "" {
		return usageError("%s returned HTTP %d: %s", label, status, safeError(detail))
	}
	return usageError("%s returned HTTP %d", label, status)
}

func decodeObjectForError(body []byte) (map[string]any, error) {
	var value map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func safeError(value string) string {
	var builder strings.Builder
	lastSpace := false
	for _, runeValue := range value {
		if unicode.IsControl(runeValue) {
			if !lastSpace {
				builder.WriteByte(' ')
				lastSpace = true
			}
			continue
		}
		if unicode.IsSpace(runeValue) {
			if !lastSpace {
				builder.WriteByte(' ')
				lastSpace = true
			}
			continue
		}
		builder.WriteRune(runeValue)
		lastSpace = false
		if builder.Len() >= 180 {
			break
		}
	}
	return strings.TrimSpace(builder.String())
}

func readJSONFile(path string) (map[string]any, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	var values map[string]any
	if err := decoder.Decode(&values); err != nil {
		return nil, err
	}
	return values, nil
}

func readTOMLString(path, key string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			// These settings belong to the root table, never a model/provider table.
			break
		}
		index := strings.IndexByte(line, '=')
		if index < 0 || strings.TrimSpace(line[:index]) != key {
			continue
		}
		value := strings.TrimSpace(line[index+1:])
		if len(value) >= 2 && value[0] == '"' {
			quoted, err := strconv.QuotedPrefix(value)
			if err != nil {
				return "", err
			}
			parsed, err := strconv.Unquote(quoted)
			if err != nil {
				return "", err
			}
			return strings.TrimSpace(parsed), nil
		}
		if len(value) >= 2 && value[0] == '\'' {
			if end := strings.IndexByte(value[1:], '\''); end >= 0 {
				return strings.TrimSpace(value[1 : end+1]), nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func joinURL(base, path string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}

func isNotExist(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}
