package tester

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/misty-step/cronlab/internal/definition"
	"github.com/misty-step/cronlab/internal/gateway"
)

const (
	ReportStatusPass = "PASS"
	ReportStatusFail = "FAIL"
)

type Runner struct {
	gateway gateway.GatewayClient
	logger  *slog.Logger
}

type Options struct {
	Timeout time.Duration
	DryRun  bool
}

type Report struct {
	GeneratedAt      time.Time                    `json:"generated_at"`
	DefinitionName   string                       `json:"definition_name"`
	DryRun           bool                         `json:"dry_run"`
	Status           string                       `json:"status"`
	Passed           bool                         `json:"passed"`
	Checks           []CheckResult                `json:"checks"`
	ValidationErrors []definition.ValidationError `json:"validation_errors,omitempty"`
	Run              gateway.RunResult            `json:"run"`
	RunError         string                       `json:"run_error,omitempty"`
	CleanupError     string                       `json:"cleanup_error,omitempty"`
}

type CheckResult struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Details string `json:"details,omitempty"`
}

type ValidationError struct {
	Errors []definition.ValidationError
}

func (e ValidationError) Error() string {
	if len(e.Errors) == 0 {
		return "definition validation failed"
	}
	return fmt.Sprintf("definition validation failed (%d errors)", len(e.Errors))
}

func NewRunner(gatewayClient gateway.GatewayClient, logger *slog.Logger) *Runner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{gateway: gatewayClient, logger: logger}
}

func (r *Runner) Run(ctx context.Context, def definition.Definition, opts Options) (Report, error) {
	report := Report{
		GeneratedAt:    time.Now().UTC(),
		DefinitionName: def.Name,
		DryRun:         opts.DryRun,
		Checks:         make([]CheckResult, 0),
	}

	validationErrors := definition.Validate(&def)
	if len(validationErrors) > 0 {
		report.ValidationErrors = validationErrors
		report.Status = ReportStatusFail
		report.Passed = false
		return report, ValidationError{Errors: validationErrors}
	}

	if opts.DryRun {
		report.Passed = true
		report.Status = ReportStatusPass
		report.Checks = append(report.Checks, CheckResult{Name: "dry_run", Passed: true, Details: "test run skipped"})
		return report, nil
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	r.logger.Info("creating ephemeral cron for test run", "name", def.Name)
	created, err := r.gateway.Create(ctx, def)
	if err != nil {
		report.Status = ReportStatusFail
		report.Passed = false
		report.RunError = err.Error()
		return report, fmt.Errorf("create test cron: %w", err)
	}

	cleanupDone := false
	defer func() {
		if cleanupDone {
			return
		}
		if err := r.gateway.Delete(context.Background(), created.ID); err != nil {
			report.CleanupError = err.Error()
			r.logger.Warn("failed to cleanup test cron", "id", created.ID, "error", err)
		}
	}()

	r.logger.Info("running test cron", "id", created.ID)
	runResult, err := r.gateway.Run(ctx, created.ID)
	if err != nil {
		report.RunError = err.Error()
		report.Status = ReportStatusFail
		report.Passed = false
		return report, fmt.Errorf("run test cron: %w", err)
	}
	report.Run = runResult

	checks := evaluate(def.Expected, runResult)
	report.Checks = checks
	report.Passed = true
	for _, c := range checks {
		if !c.Passed {
			report.Passed = false
			break
		}
	}
	if report.Passed {
		report.Status = ReportStatusPass
	} else {
		report.Status = ReportStatusFail
	}

	if err := r.gateway.Delete(ctx, created.ID); err != nil {
		report.CleanupError = err.Error()
		r.logger.Warn("failed to cleanup test cron", "id", created.ID, "error", err)
	} else {
		cleanupDone = true
	}

	return report, nil
}

func SaveReport(path string, report Report) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("report path is required")
	}
	path = expandTilde(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create report directory: %w", err)
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create report file: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	return nil
}

func LoadReport(path string) (Report, error) {
	path = expandTilde(path)
	f, err := os.Open(path)
	if err != nil {
		return Report{}, fmt.Errorf("open report file: %w", err)
	}
	defer f.Close()

	var report Report
	if err := json.NewDecoder(f).Decode(&report); err != nil {
		return Report{}, fmt.Errorf("decode report: %w", err)
	}
	return report, nil
}

func evaluate(expected definition.Expected, run gateway.RunResult) []CheckResult {
	checks := []CheckResult{
		{Name: "run_status_success", Passed: strings.EqualFold(run.Status, "success"), Details: fmt.Sprintf("status=%s", run.Status)},
		{Name: "exit_code_zero", Passed: run.ExitCode == 0, Details: fmt.Sprintf("exit_code=%d", run.ExitCode)},
	}

	outputText := strings.ToLower(run.Output + "\n" + run.Error + "\n" + run.Stdout + "\n" + run.Stderr)

	if expected.MaxDuration.Duration > 0 {
		limitMs := expected.MaxDuration.Milliseconds()
		checks = append(checks, CheckResult{
			Name:    "max_duration",
			Passed:  run.DurationMS <= limitMs,
			Details: fmt.Sprintf("duration_ms=%d limit_ms=%d", run.DurationMS, limitMs),
		})
	}

	for _, token := range expected.MustContain {
		tokenTrim := strings.TrimSpace(token)
		if tokenTrim == "" {
			continue
		}
		checks = append(checks, CheckResult{
			Name:    "must_contain",
			Passed:  strings.Contains(outputText, strings.ToLower(tokenTrim)),
			Details: tokenTrim,
		})
	}
	for _, token := range expected.MustNotContain {
		tokenTrim := strings.TrimSpace(token)
		if tokenTrim == "" {
			continue
		}
		checks = append(checks, CheckResult{
			Name:    "must_not_contain",
			Passed:  !strings.Contains(outputText, strings.ToLower(tokenTrim)),
			Details: tokenTrim,
		})
	}

	if strings.TrimSpace(run.Error) != "" || strings.TrimSpace(run.Stderr) != "" {
		checks = append(checks, CheckResult{
			Name:    "stderr_empty",
			Passed:  strings.TrimSpace(run.Error) == "" && strings.TrimSpace(run.Stderr) == "",
			Details: "run.error and run.stderr should be empty",
		})
	}

	return checks
}

func expandTilde(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[2:])
}
