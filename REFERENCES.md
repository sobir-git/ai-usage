# References and credits

`ai-usage` is an original implementation. The projects below informed the
provider research, protocol identification, and comparison work. No source
code from them is vendored or required at runtime.

## Protocol and provider references

- [OpenUsage, terminal dashboard](https://github.com/janekbaraniewski/openusage)
  by Janek Baraniewski. Its Codex provider documentation and source informed
  the live Codex usage route, the `account/rateLimits/read` app-server fallback,
  and the distinction between live quota and local session analytics.
  OpenUsage is MIT-licensed.
- [OpenUsage, macOS menu-bar app](https://github.com/robinebers/openusage)
  by Robin Ebers. Its Devin client source was inspected to identify the
  `GetUserStatus` service path and request metadata. OpenUsage is MIT-licensed.
- [OpenAI Codex CLI](https://github.com/openai/codex) and the [Codex CLI
  documentation](https://developers.openai.com/codex/cli/) informed the local
  `CODEX_HOME` layout and app-server invocation.
- [Claude Code documentation](https://docs.anthropic.com/en/docs/claude-code)
  and the [ccusage project](https://github.com/bingenroo/ccusage) informed the
  Claude Code credential layout, live usage boundary, and local-log comparison.
- [golang.org/x/term](https://pkg.go.dev/golang.org/x/term) provides the small
  terminal-mode helper used by the interactive view. It is compiled into the
  binary and is not a runtime installation requirement.

## Comparison references

- [ccusage](https://github.com/bingenroo/ccusage): local token, session, and
  cost analytics across coding agents.
- [ccgauge](https://github.com/chengzuopeng/ccgauge): local Claude Code and
  Codex usage dashboard comparison.
- [codex-usage-dashboard](https://github.com/YUHAO-corn/codex-usage-dashboard):
  local Codex log dashboard comparison.

## Important boundary

The Claude Code and Codex subscription quota routes are client interfaces that
are not published as stable public APIs. They can change when the upstream
clients change. `ai-usage` keeps the adapters small, does not cache or modify
credentials, and never sends usage data to a third-party aggregation service.
