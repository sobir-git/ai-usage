# ai-usage

One small, read-only native terminal app for live subscription usage across
Devin, Codex, and Claude Code. It is distributed as one compiled executable:
no Python installation, virtualenv, shell runtime, or background service is
needed on the machine where it runs.

## Use it

```sh
ai-usage
```

When attached to a terminal, this opens the interactive view:

- `r` refreshes all providers
- Arrow keys or `j` / `k` scroll; Space pages down; `g` / `G` jump to top / bottom
- `q` exits

The dashboard puts Devin's daily quota first, uses bars showing **remaining**
quota consistently, and shows each Codex profile separately. Colors turn amber
at 25% remaining and red at 10%. Set `NO_COLOR=1` to disable colors.

Results appear as each provider finishes. Refresh keeps the previous values on
screen and marks pending providers; pressing `r` while a refresh is running is
ignored. Quit stays responsive during network requests. The layout adapts to
terminal size, scrolls when needed, and restores the previous terminal screen on
exit. Reset countdowns update without making additional network requests. Under
12 hours, resets show hours/minutes; later resets use local dates and times.

Example dashboard (illustrative values):

```text
  AI USAGE  /  subscription quotas
  Updated 1:51:11 PM  |  local time +05
-------------------------------------------------------------------------------
  DEVIN  /  Pro
    Daily     [||||||||||..]  84.0% left   resets Sep 13, 1:00 PM
    Weekly    [||||........]  35.0% left   resets Sep 13, 1:00 PM

  CODEX  /  default  /  Pro
    7d        [||||||||||||] 100.0% left   resets Sep 19, 1:10 PM

  CODEX  /  codex-2  /  Plus
    5h        [||||||||||||]  99.0% left   resets in 4h 54m
    7d        [||||||||||||] 100.0% left   resets Sep 19, 1:45 PM

  CLAUDE CODE  /  Max
    5h        [||||||||||||] 100.0% left   reset unknown
    7d        [|||||||||||.]  95.0% left   resets Sep 17, 1:00 PM
-------------------------------------------------------------------------------
  r refresh   j/k or arrows scroll   q quit
```

For scripts or a single snapshot:

```sh
ai-usage --once
ai-usage --json
ai-usage --provider codex --provider claude
ai-usage --show-missing
ai-usage --strict
```

Configured providers are fetched concurrently. A failed provider or Codex
profile does not hide successful results. `--strict` returns failure when any
configured provider/account cannot be read.
HTTP 429 responses are reported immediately instead of retrying rapidly.
`TERM=dumb`, redirected output, and `--once` use plain text.

## Providers

### Devin

Reads Devin's local credentials from
`~/.local/share/devin/credentials.toml` and calls the account status service. It
shows daily and weekly quota, reset times in the local timezone, credits, extra
usage, and plan.

### Codex

Looks only at direct entries in the home directory: `~/.codex` and
`~/.codex-*`. Every profile with its own regular `auth.json` is queried
separately and shown by its directory label, such as `default` and `codex-2`.
Symlinked profiles and credential files are skipped.

The normal path is one live request to Codex's usage service. If that fails,
the app asks the local Codex app-server for rate limits. Human output shows the
overall quota; model-specific windows remain available in `--json`.

Codex exposes rolling windows such as five hours and seven days, not a separate
calendar-day quota.

### Claude Code

Reads `~/.claude/.credentials.json` and calls Claude Code's live OAuth usage
service. It shows rolling five-hour and seven-day windows, model-specific
windows when present, and extra-usage state.

## Provider routes

The adapters currently use these client routes:

- Devin: `POST {api_server_url}/exa.seat_management_pb.SeatManagementService/GetUserStatus`
- Codex: `GET {base_url}/wham/usage` for the normal ChatGPT backend; custom
  bases use `{base_url}/api/codex/usage`
- Codex fallback: local `codex app-server`, request
  `account/rateLimits/read`
- Claude Code: `GET https://api.anthropic.com/api/oauth/usage`

These are implementation details of the upstream clients, not guaranteed public
APIs. The exact configured Devin and Codex bases are read locally and are never
hard-coded into credentials or sent anywhere else.

## Credential handling

The app reads existing local credentials but does not refresh, modify, or
persist them. It disables HTTP redirects, limits response size, and only emits
normalized usage data. Credentials and raw provider responses never go to a
third-party service.

The provider usage routes are client interfaces rather than stable public APIs;
they may change when the upstream CLIs change. See [REFERENCES.md](REFERENCES.md)
for research and credits.

## Build and install

Requires Go only for building. The installed result is a standalone native
binary:

```sh
make build
make install
```

`make install` places the binary at `~/.local/bin/ai-usage`. No `codex-usage`
or `devin-usage` command is installed.
