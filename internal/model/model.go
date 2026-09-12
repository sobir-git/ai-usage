package model

// Window is a normalized quota window shared by Codex and Claude Code.
type Window struct {
	Name             string  `json:"name"`
	UsedPercent      float64 `json:"used_percent"`
	RemainingPercent float64 `json:"remaining_percent"`
	WindowSeconds    *int64  `json:"window_seconds,omitempty"`
	ResetAt          *string `json:"reset_at"`
	ResetAtLocal     *string `json:"reset_at_local"`
}

// Credits is the normalized Codex credits object.
type Credits struct {
	Balance    any   `json:"balance"`
	HasCredits *bool `json:"has_credits"`
	Unlimited  *bool `json:"unlimited"`
}

// ExtraUsage is the normalized Claude Code extra-usage object.
type ExtraUsage struct {
	Enabled           *bool    `json:"enabled"`
	Utilization       *float64 `json:"utilization"`
	SpendLimitReached *bool    `json:"spend_limit_reached"`
}

type CodexUsage struct {
	Endpoint  string   `json:"endpoint"`
	FetchedAt string   `json:"fetched_at"`
	Plan      *string  `json:"plan"`
	Windows   []Window `json:"windows"`
	Credits   *Credits `json:"credits"`
}

type ClaudeUsage struct {
	Endpoint   string      `json:"endpoint"`
	FetchedAt  string      `json:"fetched_at"`
	Plan       *string     `json:"plan"`
	Windows    []Window    `json:"windows"`
	ExtraUsage *ExtraUsage `json:"extra_usage"`
}

type DevinUsage struct {
	Endpoint                   string   `json:"endpoint"`
	FetchedAt                  string   `json:"fetched_at"`
	Plan                       *string  `json:"plan"`
	DailyRemainingPercent      *float64 `json:"daily_remaining_percent"`
	DailyUsedPercent           *float64 `json:"daily_used_percent"`
	DailyRemainingInferredZero bool     `json:"daily_remaining_inferred_zero"`
	DailyResetAt               *string  `json:"daily_reset_at"`
	DailyResetAtLocal          *string  `json:"daily_reset_at_local"`
	WeeklyRemainingPercent     *float64 `json:"weekly_remaining_percent"`
	WeeklyUsedPercent          *float64 `json:"weekly_used_percent"`
	WeeklyResetAt              *string  `json:"weekly_reset_at"`
	WeeklyResetAtLocal         *string  `json:"weekly_reset_at_local"`
	OverageBalanceDollars      *float64 `json:"overage_balance_dollars"`
	AvailablePromptCredits     *float64 `json:"available_prompt_credits"`
	AvailableFlowCredits       *float64 `json:"available_flow_credits"`
	AvailableFlexCredits       *float64 `json:"available_flex_credits"`
}

type CodexAccount struct {
	Profile string      `json:"profile"`
	Status  string      `json:"status"`
	Usage   *CodexUsage `json:"usage,omitempty"`
	Error   string      `json:"error,omitempty"`
}

type CodexAggregate struct {
	Accounts []CodexAccount `json:"accounts"`
}
