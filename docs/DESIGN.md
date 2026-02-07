# CronLab Design Document

## Vision

CronLab is a standalone Go binary for cron lifecycle management. It ensures that no cron job is ever deployed without validation, testing, and review. Every cron gets activity logging, error capture, and screaming failure alerts.

**Philosophy:** Never create crons raw. Every cron goes through the factory.

## Problem

We've had too many OpenClaw cron jobs that silently fail, produce unexpected output, or break without anyone noticing. Crons are set up ad-hoc, never tested, and have no observability. This is unacceptable for a high-quality tech factory.

## Architecture

CronLab is a single focused binary following UNIX principles:
- Does ONE thing well: cron lifecycle management
- Composes with other tools via stdin/stdout/exit codes
- Structured output (JSON) for machine consumption, pretty output for humans

## Commands

### `cronlab validate <definition.yaml>`
- Validate cron definition against OpenClaw cron schema
- Check schedule expressions (cron, interval, one-shot)
- Verify payload types (systemEvent, agentTurn)
- Validate session targets (main requires systemEvent, isolated requires agentTurn)
- Exit 0 if valid, exit 1 with structured errors if invalid

### `cronlab test <definition.yaml>`
- Trigger a one-shot test run of the cron
- Capture output (stdout, stderr, exit status, duration)
- Compare against expected behavior defined in the YAML
- Write results to structured test report (JSON)
- Support `--timeout` for controlling max test duration

### `cronlab review <test-report.json>`
- Send test output to an AI reviewer for quality check
- Configurable reviewer model (default: DeepSeek v3.2 via OpenRouter, or Kimi K2.5)
- Reviewer checks: does output match expected behavior? Any anomalies? Error patterns?
- Returns PASS/FAIL with reviewer commentary
- Support `--model` flag for reviewer selection

### `cronlab deploy <definition.yaml>`
- Deploy validated cron to OpenClaw
- REQUIRES prior successful `validate` + `test` + `review` (enforced)
- `--dry-run` by default, `--execute` to actually deploy
- Creates/updates cron via OpenClaw gateway API
- Records deployment in activity ledger

### `cronlab audit`
- Scan all active OpenClaw crons
- Check last run status, failure rate, activity gaps
- Flag: silent failures, disabled but not removed, missing activity logs
- Output: health report (JSON or pretty table)
- Support `--fix` to auto-remediate common issues

### `cronlab activity`
- Query the JSONL activity ledger
- Filter by cron name, date range, status
- Summary statistics: success rate, avg duration, failure patterns

## Cron Definition Format (YAML)

```yaml
name: morning-brief
description: "Daily morning briefing sent to Telegram"
schedule:
  kind: cron
  expr: "0 9 * * *"
  tz: America/Chicago
payload:
  kind: agentTurn
  message: "Generate and send the morning brief"
  model: openrouter/moonshotai/kimi-k2.5
sessionTarget: isolated
delivery:
  mode: announce
expected:
  description: "Should produce a morning brief message with date, weather, tasks, and calendar"
  maxDuration: 120s
  mustContain: ["morning", "brief"]
  mustNotContain: ["error", "failed"]
tags:
  - daily
  - briefing
```

## Activity Ledger (JSONL)

Every cron execution appends to `~/.cronlab/activity.jsonl`:

```json
{"ts":"2026-02-07T15:00:00Z","cron":"morning-brief","status":"success","duration_ms":4500,"output_bytes":1234}
{"ts":"2026-02-07T15:30:00Z","cron":"conscience","status":"error","duration_ms":30000,"error":"timeout exceeded"}
```

## Configuration

`~/.cronlab/config.yaml`:

```yaml
gateway:
  url: http://localhost:4152  # or whatever the gateway URL is
reviewer:
  model: openrouter/deepseek/deepseek-chat-v3-0324
  fallback: openrouter/moonshotai/kimi-k2.5
activity:
  path: ~/.cronlab/activity.jsonl
  retention: 90d
alerts:
  telegram: true
  threshold: 2  # alert after N consecutive failures
```

## Non-Goals (v1)
- Custom cron scheduler (we use OpenClaw's)
- Web UI (that's Overmind's job)
- Distributed execution
- Real-time monitoring (use `bb watch` for that)

## Dependencies
- `github.com/spf13/cobra` — CLI framework
- `gopkg.in/yaml.v3` — YAML parsing
- `encoding/json` — JSONL handling (stdlib)
- HTTP client for OpenClaw gateway API (stdlib `net/http`)
- HTTP client for AI reviewer API (stdlib `net/http`)

## Testing Strategy
- Unit tests for validation, parsing, ledger operations
- Integration tests for the full validate→test→review→deploy pipeline
- Mock OpenClaw gateway for testing deploy/audit
- >80% coverage target
