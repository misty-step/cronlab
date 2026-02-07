package auditor

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/misty-step/cronlab/internal/gateway"
	"github.com/misty-step/cronlab/internal/ledger"
)

type ActivityStore interface {
	Query(ctx context.Context, filter ledger.QueryFilter) ([]ledger.Entry, error)
	Append(ctx context.Context, entry ledger.Entry) error
}

type Auditor struct {
	gateway gateway.GatewayClient
	ledger  ActivityStore
	logger  *slog.Logger
}

type Options struct {
	Lookback                    time.Duration
	GapThreshold                time.Duration
	FailureRateThreshold        float64
	ConsecutiveFailureThreshold int
	Fix                         bool
	DryRun                      bool
}

type Severity string

const (
	SeverityHealthy  Severity = "healthy"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

type Issue struct {
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
}

type CronHealth struct {
	Cron                 gateway.Cron `json:"cron"`
	Issues               []Issue      `json:"issues,omitempty"`
	Severity             Severity     `json:"severity"`
	LastActivity         *time.Time   `json:"last_activity,omitempty"`
	EntriesChecked       int          `json:"entries_checked"`
	FailureRate          float64      `json:"failure_rate"`
	ConsecutiveFailures  int          `json:"consecutive_failures"`
	RecentFailurePattern []string     `json:"recent_failure_pattern,omitempty"`
}

type FixResult struct {
	CronID    string `json:"cron_id"`
	CronName  string `json:"cron_name"`
	Action    string `json:"action"`
	Applied   bool   `json:"applied"`
	Message   string `json:"message,omitempty"`
	RunStatus string `json:"run_status,omitempty"`
}

type Report struct {
	GeneratedAt time.Time    `json:"generated_at"`
	DryRun      bool         `json:"dry_run"`
	Total       int          `json:"total"`
	Healthy     int          `json:"healthy"`
	Warning     int          `json:"warning"`
	Critical    int          `json:"critical"`
	Crons       []CronHealth `json:"crons"`
	Fixes       []FixResult  `json:"fixes,omitempty"`
}

func New(gw gateway.GatewayClient, activity ActivityStore, logger *slog.Logger) *Auditor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Auditor{gateway: gw, ledger: activity, logger: logger}
}

func DefaultOptions() Options {
	return Options{
		Lookback:                    7 * 24 * time.Hour,
		GapThreshold:                48 * time.Hour,
		FailureRateThreshold:        0.5,
		ConsecutiveFailureThreshold: 2,
		Fix:                         false,
		DryRun:                      true,
	}
}

func (a *Auditor) Audit(ctx context.Context, opts Options) (Report, error) {
	if opts.Lookback <= 0 {
		opts.Lookback = 7 * 24 * time.Hour
	}
	if opts.GapThreshold <= 0 {
		opts.GapThreshold = 48 * time.Hour
	}
	if opts.FailureRateThreshold <= 0 {
		opts.FailureRateThreshold = 0.5
	}
	if opts.ConsecutiveFailureThreshold <= 0 {
		opts.ConsecutiveFailureThreshold = 2
	}

	report := Report{
		GeneratedAt: time.Now().UTC(),
		DryRun:      opts.DryRun,
		Crons:       make([]CronHealth, 0),
		Fixes:       make([]FixResult, 0),
	}

	crons, err := a.gateway.List(ctx)
	if err != nil {
		return report, fmt.Errorf("list crons: %w", err)
	}
	if len(crons) == 0 {
		return report, nil
	}

	from := time.Now().UTC().Add(-opts.Lookback)

	for _, cron := range crons {
		if err := ctx.Err(); err != nil {
			return report, err
		}

		health, err := a.scanCron(ctx, cron, from, opts)
		if err != nil {
			return report, fmt.Errorf("scan cron %s: %w", cron.ID, err)
		}
		report.Crons = append(report.Crons, health)
		report.Total++

		switch health.Severity {
		case SeverityCritical:
			report.Critical++
		case SeverityWarning:
			report.Warning++
		default:
			report.Healthy++
		}

		if opts.Fix && health.Severity != SeverityHealthy {
			fixes := a.applyFixes(ctx, cron, health, opts)
			report.Fixes = append(report.Fixes, fixes...)
		}
	}

	sort.Slice(report.Crons, func(i, j int) bool {
		if report.Crons[i].Severity != report.Crons[j].Severity {
			return report.Crons[i].Severity > report.Crons[j].Severity
		}
		return report.Crons[i].Cron.Name < report.Crons[j].Cron.Name
	})

	return report, nil
}

func (a *Auditor) scanCron(ctx context.Context, cron gateway.Cron, from time.Time, opts Options) (CronHealth, error) {
	entries, err := a.ledger.Query(ctx, ledger.QueryFilter{Cron: cron.Name, From: &from})
	if err != nil {
		return CronHealth{}, err
	}

	health := CronHealth{
		Cron:           cron,
		Severity:       SeverityHealthy,
		Issues:         make([]Issue, 0),
		EntriesChecked: len(entries),
	}

	if !cron.Enabled {
		health.Issues = append(health.Issues, Issue{
			Code:     "disabled_not_removed",
			Severity: SeverityCritical,
			Message:  "Cron is disabled but still present in gateway",
		})
	}

	if len(entries) == 0 {
		health.Issues = append(health.Issues, Issue{
			Code:     "missing_activity_logs",
			Severity: SeverityWarning,
			Message:  "No activity found in lookback window",
		})
		health.Severity = maxSeverity(health.Issues)
		return health, nil
	}

	last := entries[len(entries)-1].Timestamp
	health.LastActivity = &last

	now := time.Now().UTC()
	if now.Sub(last) > opts.GapThreshold {
		health.Issues = append(health.Issues, Issue{
			Code:     "activity_gap",
			Severity: SeverityWarning,
			Message:  fmt.Sprintf("Last activity is older than threshold: %s", now.Sub(last).Round(time.Minute)),
		})
	}

	failures := 0
	patterns := make([]string, 0)
	for _, entry := range entries {
		if !isSuccess(entry.Status) {
			failures++
			pattern := strings.TrimSpace(entry.Error)
			if pattern == "" {
				pattern = entry.Status
			}
			patterns = append(patterns, pattern)
		}
	}
	if len(entries) > 0 {
		health.FailureRate = float64(failures) / float64(len(entries))
	}
	health.ConsecutiveFailures = trailingFailures(entries)
	health.RecentFailurePattern = compactPatterns(patterns, 3)

	if health.FailureRate >= opts.FailureRateThreshold || health.ConsecutiveFailures >= opts.ConsecutiveFailureThreshold {
		health.Issues = append(health.Issues, Issue{
			Code:     "silent_failures",
			Severity: SeverityCritical,
			Message:  fmt.Sprintf("Failure rate %.2f with %d consecutive failures", health.FailureRate, health.ConsecutiveFailures),
		})
	}

	health.Severity = maxSeverity(health.Issues)
	return health, nil
}

func (a *Auditor) applyFixes(ctx context.Context, cron gateway.Cron, health CronHealth, opts Options) []FixResult {
	fixes := make([]FixResult, 0)
	if containsIssue(health.Issues, "disabled_not_removed") {
		fixes = append(fixes, FixResult{
			CronID:   cron.ID,
			CronName: cron.Name,
			Action:   "manual_cleanup",
			Applied:  false,
			Message:  "No gateway API to remove disabled cron automatically",
		})
	}

	shouldRun := containsIssue(health.Issues, "missing_activity_logs") || containsIssue(health.Issues, "activity_gap") || containsIssue(health.Issues, "silent_failures")
	if !shouldRun || !cron.Enabled {
		return fixes
	}

	if opts.DryRun {
		fixes = append(fixes, FixResult{
			CronID:   cron.ID,
			CronName: cron.Name,
			Action:   "run_trigger",
			Applied:  false,
			Message:  "Dry-run enabled; no remediation executed",
		})
		return fixes
	}

	runResult, err := a.gateway.Run(ctx, cron.ID)
	if err != nil {
		fixes = append(fixes, FixResult{
			CronID:   cron.ID,
			CronName: cron.Name,
			Action:   "run_trigger",
			Applied:  false,
			Message:  err.Error(),
		})
		_ = a.ledger.Append(context.Background(), ledger.Entry{
			Timestamp:  time.Now().UTC(),
			Cron:       cron.Name,
			Status:     "error",
			Action:     "audit_fix_run",
			Error:      err.Error(),
			DurationMS: 0,
		})
		return fixes
	}

	fixes = append(fixes, FixResult{
		CronID:    cron.ID,
		CronName:  cron.Name,
		Action:    "run_trigger",
		Applied:   true,
		RunStatus: runResult.Status,
	})
	_ = a.ledger.Append(context.Background(), ledger.Entry{
		Timestamp:   time.Now().UTC(),
		Cron:        cron.Name,
		Status:      runResult.Status,
		DurationMS:  runResult.DurationMS,
		OutputBytes: int64(len(runResult.Output) + len(runResult.Stdout) + len(runResult.Stderr)),
		Error:       runResult.Error,
		Action:      "audit_fix_run",
	})

	return fixes
}

func containsIssue(issues []Issue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func isSuccess(status string) bool {
	return strings.EqualFold(strings.TrimSpace(status), "success")
}

func trailingFailures(entries []ledger.Entry) int {
	count := 0
	for i := len(entries) - 1; i >= 0; i-- {
		if isSuccess(entries[i].Status) {
			break
		}
		count++
	}
	return count
}

func compactPatterns(patterns []string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, limit)
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if _, exists := seen[pattern]; exists {
			continue
		}
		seen[pattern] = struct{}{}
		out = append(out, pattern)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func maxSeverity(issues []Issue) Severity {
	severity := SeverityHealthy
	for _, issue := range issues {
		switch issue.Severity {
		case SeverityCritical:
			return SeverityCritical
		case SeverityWarning:
			if severity != SeverityCritical {
				severity = SeverityWarning
			}
		}
	}
	return severity
}
