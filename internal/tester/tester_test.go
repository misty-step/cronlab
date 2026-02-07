package tester

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/misty-step/cronlab/internal/definition"
	"github.com/misty-step/cronlab/internal/gateway"
)

func validDefinition() definition.Definition {
	return definition.Definition{
		Name:          "morning-brief",
		Schedule:      definition.Schedule{Kind: definition.ScheduleKindCron, Expr: "0 9 * * *"},
		Payload:       definition.Payload{Kind: definition.PayloadKindAgentTurn, Message: "Generate"},
		SessionTarget: definition.SessionTargetIsolated,
		Expected: definition.Expected{
			MaxDuration:    definition.Duration{Duration: 2 * time.Second},
			MustContain:    []string{"morning", "brief"},
			MustNotContain: []string{"error"},
		},
	}
}

func TestRunnerDryRun(t *testing.T) {
	t.Parallel()

	called := false
	mock := gateway.NewMockClient()
	mock.CreateFunc = func(ctx context.Context, def definition.Definition) (gateway.Cron, error) {
		called = true
		return gateway.Cron{}, nil
	}

	runner := NewRunner(mock, nil)
	report, err := runner.Run(context.Background(), validDefinition(), Options{DryRun: true})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if called {
		t.Fatalf("gateway Create() should not be called in dry-run")
	}
	if !report.Passed || report.Status != ReportStatusPass || !report.DryRun {
		t.Fatalf("report = %+v, want dry-run pass", report)
	}
}

func TestRunnerPass(t *testing.T) {
	t.Parallel()

	var deleted string
	mock := gateway.NewMockClient()
	mock.CreateFunc = func(ctx context.Context, def definition.Definition) (gateway.Cron, error) {
		return gateway.Cron{ID: "cron-1", Name: def.Name}, nil
	}
	mock.RunFunc = func(ctx context.Context, id string) (gateway.RunResult, error) {
		return gateway.RunResult{
			CronID:     id,
			Status:     "success",
			Stdout:     "Morning brief generated",
			ExitCode:   0,
			DurationMS: 1200,
			StartedAt:  time.Now().UTC(),
			FinishedAt: time.Now().UTC(),
		}, nil
	}
	mock.DeleteFunc = func(ctx context.Context, id string) error {
		deleted = id
		return nil
	}

	runner := NewRunner(mock, nil)
	report, err := runner.Run(context.Background(), validDefinition(), Options{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !report.Passed || report.Status != ReportStatusPass {
		t.Fatalf("report = %+v, want PASS", report)
	}
	if deleted != "cron-1" {
		t.Fatalf("deleted id = %q, want cron-1", deleted)
	}
}

func TestRunnerFailOnExpectation(t *testing.T) {
	t.Parallel()

	mock := gateway.NewMockClient()
	mock.CreateFunc = func(ctx context.Context, def definition.Definition) (gateway.Cron, error) {
		return gateway.Cron{ID: "cron-1", Name: def.Name}, nil
	}
	mock.RunFunc = func(ctx context.Context, id string) (gateway.RunResult, error) {
		return gateway.RunResult{
			CronID:     id,
			Status:     "success",
			Stdout:     "unexpected output with error keyword",
			ExitCode:   0,
			DurationMS: 3000,
			StartedAt:  time.Now().UTC(),
			FinishedAt: time.Now().UTC(),
		}, nil
	}
	mock.DeleteFunc = func(ctx context.Context, id string) error { return nil }

	def := validDefinition()
	runner := NewRunner(mock, nil)
	report, err := runner.Run(context.Background(), def, Options{})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if report.Passed || report.Status != ReportStatusFail {
		t.Fatalf("report = %+v, want FAIL", report)
	}
}

func TestRunnerValidationError(t *testing.T) {
	t.Parallel()

	runner := NewRunner(gateway.NewMockClient(), nil)
	_, err := runner.Run(context.Background(), definition.Definition{Name: "x"}, Options{})
	if err == nil {
		t.Fatalf("Run() expected validation error")
	}
	var vErr ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("Run() err = %v, want ValidationError", err)
	}
}

func TestRunnerTimeout(t *testing.T) {
	t.Parallel()

	mock := gateway.NewMockClient()
	mock.CreateFunc = func(ctx context.Context, def definition.Definition) (gateway.Cron, error) {
		return gateway.Cron{ID: "cron-1", Name: def.Name}, nil
	}
	mock.RunFunc = func(ctx context.Context, id string) (gateway.RunResult, error) {
		<-ctx.Done()
		return gateway.RunResult{}, ctx.Err()
	}
	mock.DeleteFunc = func(ctx context.Context, id string) error { return nil }

	runner := NewRunner(mock, nil)
	_, err := runner.Run(context.Background(), validDefinition(), Options{Timeout: 10 * time.Millisecond})
	if err == nil {
		t.Fatalf("Run() expected timeout error")
	}
}

func TestSaveAndLoadReport(t *testing.T) {
	t.Parallel()

	report := Report{
		GeneratedAt:    time.Date(2026, 2, 7, 15, 0, 0, 0, time.UTC),
		DefinitionName: "morning-brief",
		Status:         ReportStatusPass,
		Passed:         true,
		Checks: []CheckResult{{
			Name:   "run_status_success",
			Passed: true,
		}},
	}

	path := filepath.Join(t.TempDir(), "report.json")
	if err := SaveReport(path, report); err != nil {
		t.Fatalf("SaveReport() error = %v", err)
	}

	loaded, err := LoadReport(path)
	if err != nil {
		t.Fatalf("LoadReport() error = %v", err)
	}
	if loaded.DefinitionName != report.DefinitionName || loaded.Status != report.Status || !loaded.Passed {
		t.Fatalf("loaded = %+v, want %+v", loaded, report)
	}
}
