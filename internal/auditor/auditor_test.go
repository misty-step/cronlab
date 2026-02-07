package auditor

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/misty-step/cronlab/internal/gateway"
	"github.com/misty-step/cronlab/internal/ledger"
)

func newLedger(t *testing.T) *ledger.Store {
	t.Helper()
	return ledger.New(filepath.Join(t.TempDir(), "activity.jsonl"))
}

func appendEntry(t *testing.T, store *ledger.Store, entry ledger.Entry) {
	t.Helper()
	if err := store.Append(context.Background(), entry); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
}

func TestAuditHealthyCron(t *testing.T) {
	t.Parallel()

	store := newLedger(t)
	now := time.Now().UTC()
	appendEntry(t, store, ledger.Entry{Timestamp: now.Add(-2 * time.Hour), Cron: "morning-brief", Status: "success", DurationMS: 1000})

	mock := gateway.NewMockClient()
	mock.ListFunc = func(ctx context.Context) ([]gateway.Cron, error) {
		return []gateway.Cron{{ID: "c1", Name: "morning-brief", Enabled: true}}, nil
	}

	a := New(mock, store, nil)
	report, err := a.Audit(context.Background(), DefaultOptions())
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	if report.Total != 1 || report.Healthy != 1 || report.Warning != 0 || report.Critical != 0 {
		t.Fatalf("report summary = %+v, want healthy=1", report)
	}
	if len(report.Crons) != 1 || report.Crons[0].Severity != SeverityHealthy {
		t.Fatalf("cron health = %+v, want healthy", report.Crons)
	}
}

func TestAuditMissingActivityLogs(t *testing.T) {
	t.Parallel()

	store := newLedger(t)
	mock := gateway.NewMockClient()
	mock.ListFunc = func(ctx context.Context) ([]gateway.Cron, error) {
		return []gateway.Cron{{ID: "c1", Name: "morning-brief", Enabled: true}}, nil
	}

	a := New(mock, store, nil)
	report, err := a.Audit(context.Background(), DefaultOptions())
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	if report.Warning != 1 {
		t.Fatalf("report warning count = %d, want 1", report.Warning)
	}
	if !hasIssue(report.Crons[0].Issues, "missing_activity_logs") {
		t.Fatalf("issues = %+v, want missing_activity_logs", report.Crons[0].Issues)
	}
}

func TestAuditDetectsSilentFailuresAndGap(t *testing.T) {
	t.Parallel()

	store := newLedger(t)
	now := time.Now().UTC()
	appendEntry(t, store, ledger.Entry{Timestamp: now.Add(-72 * time.Hour), Cron: "morning-brief", Status: "error", Error: "timeout"})
	appendEntry(t, store, ledger.Entry{Timestamp: now.Add(-71 * time.Hour), Cron: "morning-brief", Status: "error", Error: "timeout"})

	mock := gateway.NewMockClient()
	mock.ListFunc = func(ctx context.Context) ([]gateway.Cron, error) {
		return []gateway.Cron{{ID: "c1", Name: "morning-brief", Enabled: true}}, nil
	}

	a := New(mock, store, nil)
	opts := DefaultOptions()
	opts.GapThreshold = 24 * time.Hour
	report, err := a.Audit(context.Background(), opts)
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	if report.Critical != 1 {
		t.Fatalf("critical count = %d, want 1", report.Critical)
	}
	issues := report.Crons[0].Issues
	if !hasIssue(issues, "activity_gap") || !hasIssue(issues, "silent_failures") {
		t.Fatalf("issues = %+v, want activity_gap and silent_failures", issues)
	}
}

func TestAuditDisabledCron(t *testing.T) {
	t.Parallel()

	store := newLedger(t)
	now := time.Now().UTC()
	appendEntry(t, store, ledger.Entry{Timestamp: now.Add(-1 * time.Hour), Cron: "old-cron", Status: "success"})

	mock := gateway.NewMockClient()
	mock.ListFunc = func(ctx context.Context) ([]gateway.Cron, error) {
		return []gateway.Cron{{ID: "c2", Name: "old-cron", Enabled: false}}, nil
	}

	a := New(mock, store, nil)
	report, err := a.Audit(context.Background(), DefaultOptions())
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	if report.Critical != 1 {
		t.Fatalf("critical count = %d, want 1", report.Critical)
	}
	if !hasIssue(report.Crons[0].Issues, "disabled_not_removed") {
		t.Fatalf("issues = %+v, want disabled_not_removed", report.Crons[0].Issues)
	}
}

func TestAuditFixRunsCron(t *testing.T) {
	t.Parallel()

	store := newLedger(t)
	runCalled := false

	mock := gateway.NewMockClient()
	mock.ListFunc = func(ctx context.Context) ([]gateway.Cron, error) {
		return []gateway.Cron{{ID: "c1", Name: "morning-brief", Enabled: true}}, nil
	}
	mock.RunFunc = func(ctx context.Context, id string) (gateway.RunResult, error) {
		runCalled = true
		return gateway.RunResult{CronID: id, Status: "success", DurationMS: 1200, Output: "ok"}, nil
	}

	a := New(mock, store, nil)
	opts := DefaultOptions()
	opts.Fix = true
	opts.DryRun = false
	report, err := a.Audit(context.Background(), opts)
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	if !runCalled {
		t.Fatalf("expected run remediation to be called")
	}
	if len(report.Fixes) == 0 || !report.Fixes[0].Applied || report.Fixes[0].Action != "run_trigger" {
		t.Fatalf("fixes = %+v, want applied run_trigger", report.Fixes)
	}

	entries, err := store.Query(context.Background(), ledger.QueryFilter{Cron: "morning-brief"})
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Action != "audit_fix_run" {
		t.Fatalf("entries = %+v, want audit_fix_run", entries)
	}
}

func TestAuditFixDryRunDoesNotRunCron(t *testing.T) {
	t.Parallel()

	store := newLedger(t)
	runCalled := false

	mock := gateway.NewMockClient()
	mock.ListFunc = func(ctx context.Context) ([]gateway.Cron, error) {
		return []gateway.Cron{{ID: "c1", Name: "morning-brief", Enabled: true}}, nil
	}
	mock.RunFunc = func(ctx context.Context, id string) (gateway.RunResult, error) {
		runCalled = true
		return gateway.RunResult{CronID: id, Status: "success"}, nil
	}

	a := New(mock, store, nil)
	opts := DefaultOptions()
	opts.Fix = true
	opts.DryRun = true
	report, err := a.Audit(context.Background(), opts)
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	if runCalled {
		t.Fatalf("run should not be called in dry-run mode")
	}
	if len(report.Fixes) == 0 || report.Fixes[0].Applied {
		t.Fatalf("fixes = %+v, want non-applied dry-run remediation", report.Fixes)
	}
}

func hasIssue(issues []Issue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}
