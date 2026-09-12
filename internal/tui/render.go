package tui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/sobir-git/ai-usage/internal/app"
	"github.com/sobir-git/ai-usage/internal/model"
)

type line struct{ text, color string }

const (
	muted  = "\x1b[90m"
	accent = "\x1b[1;36m"
	good   = "\x1b[32m"
	warn   = "\x1b[33m"
	bad    = "\x1b[31m"
)

// Terminal text is restricted to printable single-cell ASCII. Neither remote
// plan names nor local profile names may inject escape sequences into a frame.
func clean(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		if r > 126 {
			return '?'
		}
		return r
	}, value)
}

func clip(value string, width int) string {
	value = clean(value)
	if width <= 0 {
		return ""
	}
	if len(value) <= width {
		return value
	}
	if width < 4 {
		return value[:width]
	}
	return value[:width-3] + "..."
}

func paint(row line, width int, color bool) string {
	value := clip(row.text, width)
	if color && row.color != "" {
		value = row.color + value + "\x1b[0m"
	}
	return value + "\x1b[K"
}

func render(results []app.ProviderResult, pending []bool, showMissing bool, refreshed time.Time, width, height, offset int, now time.Time, color bool) (string, int) {
	width = max(1, width-1) // Avoid automatic wrap in the terminal's last column.
	if height < 5 {
		return paint(line{"ai-usage: enlarge terminal | q quit", warn}, width, color), 0
	}
	var body []line
	loading := 0
	for i, result := range results {
		busy := i < len(pending) && pending[i]
		if busy {
			loading++
		}
		if result.Status == "not_configured" && !showMissing {
			continue
		}
		body = append(body, providerLines(result, busy, width, now)...)
	}
	if len(body) == 0 {
		body = []line{{"  No configured accounts found. Log in with a provider CLI.", muted}}
	}
	visible := height - 5
	maxOffset := max(0, len(body)-visible)
	offset = max(0, min(offset, maxOffset))
	status := "Ready"
	if loading > 0 {
		status = fmt.Sprintf("Refreshing %d provider(s)...", loading)
	} else if !refreshed.IsZero() {
		status = "Updated " + refreshed.Format("3:04:05 PM")
	}
	zone, _ := now.Zone()
	rows := []line{
		{"  AI USAGE  /  subscription quotas", accent},
		{"  " + status + "  |  local time " + zone, muted},
		{strings.Repeat("-", width), muted},
	}
	rows = append(rows, body[offset:min(len(body), offset+visible)]...)
	for len(rows) < height-2 {
		rows = append(rows, line{})
	}
	rows = append(rows, line{strings.Repeat("-", width), muted})
	footer := "  r refresh   j/k or arrows scroll   q quit"
	if maxOffset > 0 {
		footer += fmt.Sprintf("   %d-%d/%d", offset+1, min(len(body), offset+visible), len(body))
	}
	rows = append(rows, line{footer, muted})
	output := make([]string, len(rows))
	for i, row := range rows {
		output[i] = paint(row, width, color)
	}
	return strings.Join(output, "\n"), offset
}

func providerLines(result app.ProviderResult, busy bool, width int, now time.Time) []line {
	suffix := ""
	if busy {
		suffix = "  [refreshing]"
	}
	header := func(name string, plan *string) line {
		if plan != nil && *plan != "" {
			label := clean(*plan)
			name += "  /  " + strings.ToUpper(label[:1]) + label[1:]
		}
		return line{"  " + name + suffix, accent}
	}
	var rows []line
	switch usage := result.Usage.(type) {
	case *model.DevinUsage:
		rows = append(rows, header("DEVIN", usage.Plan))
		if usage.DailyRemainingPercent != nil {
			rows = append(rows, quota("Daily", *usage.DailyRemainingPercent, usage.DailyResetAt, width, now)...)
		} else {
			rows = append(rows, line{"    Daily quota unavailable", muted})
		}
		if usage.WeeklyRemainingPercent != nil {
			rows = append(rows, quota("Weekly", *usage.WeeklyRemainingPercent, usage.WeeklyResetAt, width, now)...)
		}
		if usage.DailyRemainingInferredZero {
			rows = append(rows, line{"    Daily zero inferred from provider's omitted value", warn})
		}
		if usage.OverageBalanceDollars != nil {
			rows = append(rows, line{fmt.Sprintf("    Extra balance $%.2f", *usage.OverageBalanceDollars), muted})
		}
		for _, credit := range []struct {
			name  string
			value *float64
		}{{"Prompt", usage.AvailablePromptCredits}, {"Flow", usage.AvailableFlowCredits}, {"Flex", usage.AvailableFlexCredits}} {
			if credit.value != nil {
				rows = append(rows, line{fmt.Sprintf("    %s credits: %.2f", credit.name, *credit.value), muted})
			}
		}
	case *model.CodexAggregate:
		for i, account := range usage.Accounts {
			if i > 0 {
				rows = append(rows, line{})
			}
			name := "CODEX  /  " + account.Profile
			if account.Status != "ok" || account.Usage == nil {
				rows = append(rows, header(name, nil))
				rows = append(rows, errorLines(account.Error, width)...)
				continue
			}
			rows = append(rows, header(name, account.Usage.Plan))
			count := 0
			for _, window := range account.Usage.Windows {
				if strings.Contains(window.Name, " (") {
					continue
				}
				rows = append(rows, quota(window.Name, window.RemainingPercent, window.ResetAt, width, now)...)
				count++
			}
			if count == 0 {
				rows = append(rows, line{"    Overall quota unavailable", warn})
			}
			if credits := account.Usage.Credits; credits != nil {
				if credits.Unlimited != nil && *credits.Unlimited {
					rows = append(rows, line{"    Credits: unlimited", muted})
				} else if credits.Balance != nil {
					balance := fmt.Sprint(credits.Balance)
					if value, err := strconv.ParseFloat(balance, 64); err != nil || value > 0 {
						rows = append(rows, line{"    Credits: " + balance, muted})
					}
				}
			}
		}
	case *model.ClaudeUsage:
		rows = append(rows, header("CLAUDE CODE", usage.Plan))
		for _, window := range usage.Windows {
			rows = append(rows, quota(window.Name, window.RemainingPercent, window.ResetAt, width, now)...)
		}
		if extra := usage.ExtraUsage; extra != nil && extra.Enabled != nil && *extra.Enabled {
			rows = append(rows, line{"    Extra usage enabled", muted})
		}
	default:
		rows = append(rows, header(strings.ToUpper(result.Name), nil))
		switch result.Status {
		case "loading":
			rows = append(rows, line{"    Fetching usage...", muted})
		case "not_configured":
			rows = append(rows, line{"    Not configured", muted})
		default:
			rows = append(rows, errorLines(result.Error, width)...)
		}
	}
	return append(rows, line{})
}

func quota(name string, remaining float64, reset *string, width int, now time.Time) []line {
	remaining = math.Max(0, math.Min(100, remaining))
	color := good
	if remaining <= 25 {
		color = warn
	}
	if remaining <= 10 {
		color = bad
	}
	name = strings.TrimSuffix(name, " secondary")
	barWidth := 12
	if width < 60 {
		barWidth = 8
	}
	filled := int(math.Round(remaining / 100 * float64(barWidth)))
	bar := strings.Repeat("|", filled) + strings.Repeat(".", barWidth-filled)
	label := fmt.Sprintf("    %-9s [%s] %5.1f%% left", clip(name, 9), bar, remaining)
	resetText := resetLabel(reset, now)
	if len(label)+len(resetText)+3 <= width {
		return []line{{label + "   " + resetText, color}}
	}
	if width < 40 {
		label = fmt.Sprintf("    %s: %.1f%% left", name, remaining)
	}
	return []line{{label, color}, {"      " + resetText, muted}}
}

func resetLabel(value *string, now time.Time) string {
	if value == nil {
		return "reset unknown"
	}
	reset, err := time.Parse(time.RFC3339Nano, *value)
	if err != nil {
		return "reset unknown"
	}
	delta := reset.Sub(now)
	if delta <= 0 {
		return "reset due; refresh to confirm"
	}
	if delta < 12*time.Hour {
		minutes := int(math.Ceil(delta.Minutes()))
		if minutes < 60 {
			return fmt.Sprintf("resets in %dm", minutes)
		}
		if minutes%60 == 0 {
			return fmt.Sprintf("resets in %dh", minutes/60)
		}
		return fmt.Sprintf("resets in %dh %dm", minutes/60, minutes%60)
	}
	return "resets " + reset.In(time.Local).Format("Jan 2, 3:04 PM")
}

func errorLines(message string, width int) []line {
	if message == "" {
		message = "Usage unavailable"
	}
	var rows []line
	current := "    "
	for _, word := range strings.Fields(clean(message)) {
		if len(current)+len(word)+1 > width && len(current) > 4 {
			rows = append(rows, line{current, bad})
			current = "    "
		}
		if len(current) > 4 {
			current += " "
		}
		current += word
	}
	return append(rows, line{current, bad})
}
