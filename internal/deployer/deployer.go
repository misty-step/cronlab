package deployer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/misty-step/cronlab/internal/definition"
	"github.com/misty-step/cronlab/internal/gateway"
	"github.com/misty-step/cronlab/internal/ledger"
	"github.com/misty-step/cronlab/internal/reviewer"
	"github.com/misty-step/cronlab/internal/tester"
)

type Stage string

const (
	StageValidate Stage = "validate"
	StageTest     Stage = "test"
	StageReview   Stage = "review"
	StageDeploy   Stage = "deploy"
)

type Tester interface {
	Run(ctx context.Context, def definition.Definition, opts tester.Options) (tester.Report, error)
}

type Reviewer interface {
	Review(ctx context.Context, report tester.Report, opts reviewer.Options) (reviewer.Result, error)
}

type ActivityLedger interface {
	Append(ctx context.Context, entry ledger.Entry) error
}

type Deployer struct {
	gateway  gateway.GatewayClient
	tester   Tester
	reviewer Reviewer
	ledger   ActivityLedger
	logger   *slog.Logger
}

type Options struct {
	DryRun  bool
	Timeout time.Duration
	Model   string
}

type Result struct {
	Timestamp  time.Time       `json:"timestamp"`
	DryRun     bool            `json:"dry_run"`
	Action     string          `json:"action"`
	CronID     string          `json:"cron_id,omitempty"`
	Definition string          `json:"definition"`
	TestReport tester.Report   `json:"test_report"`
	Review     reviewer.Result `json:"review"`
}

type GateError struct {
	Stage Stage
	Err   error
}

func (e GateError) Error() string {
	return fmt.Sprintf("%s gate failed: %v", e.Stage, e.Err)
}

func (e GateError) Unwrap() error {
	return e.Err
}

func New(gw gateway.GatewayClient, tst Tester, rev Reviewer, activity ActivityLedger, logger *slog.Logger) *Deployer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Deployer{
		gateway:  gw,
		tester:   tst,
		reviewer: rev,
		ledger:   activity,
		logger:   logger,
	}
}

func (d *Deployer) Deploy(ctx context.Context, def definition.Definition, opts Options) (Result, error) {
	started := time.Now().UTC()
	result := Result{
		Timestamp:  started,
		DryRun:     opts.DryRun,
		Definition: def.Name,
	}

	validationErrors := definition.Validate(&def)
	if len(validationErrors) > 0 {
		err := tester.ValidationError{Errors: validationErrors}
		result.Action = string(StageValidate)
		d.record(ctx, def.Name, "error", time.Since(started), err.Error(), "deploy_validate_failed")
		return result, GateError{Stage: StageValidate, Err: err}
	}

	report, err := d.tester.Run(ctx, def, tester.Options{Timeout: opts.Timeout})
	result.TestReport = report
	if err != nil {
		result.Action = string(StageTest)
		d.record(ctx, def.Name, "error", time.Since(started), err.Error(), "deploy_test_failed")
		return result, GateError{Stage: StageTest, Err: err}
	}
	if !report.Passed {
		err = errors.New("test report status is FAIL")
		result.Action = string(StageTest)
		d.record(ctx, def.Name, "error", time.Since(started), err.Error(), "deploy_test_failed")
		return result, GateError{Stage: StageTest, Err: err}
	}

	reviewResult, err := d.reviewer.Review(ctx, report, reviewer.Options{Model: opts.Model})
	result.Review = reviewResult
	if err != nil {
		result.Action = string(StageReview)
		d.record(ctx, def.Name, "error", time.Since(started), err.Error(), "deploy_review_failed")
		return result, GateError{Stage: StageReview, Err: err}
	}
	if !reviewResult.Passed {
		err = errors.New("review status is FAIL")
		result.Action = string(StageReview)
		d.record(ctx, def.Name, "error", time.Since(started), err.Error(), "deploy_review_failed")
		return result, GateError{Stage: StageReview, Err: err}
	}

	if opts.DryRun {
		result.Action = "dry-run"
		d.record(ctx, def.Name, "success", time.Since(started), "", "deploy_dry_run")
		return result, nil
	}

	action, cronID, err := d.apply(ctx, def)
	result.Action = action
	result.CronID = cronID
	if err != nil {
		d.record(ctx, def.Name, "error", time.Since(started), err.Error(), "deploy_apply_failed")
		return result, GateError{Stage: StageDeploy, Err: err}
	}

	d.record(ctx, def.Name, "success", time.Since(started), "", "deploy_"+action)
	return result, nil
}

func (d *Deployer) apply(ctx context.Context, def definition.Definition) (string, string, error) {
	crons, err := d.gateway.List(ctx)
	if err != nil {
		return "", "", fmt.Errorf("list crons: %w", err)
	}

	for _, cron := range crons {
		if strings.EqualFold(cron.Name, def.Name) {
			d.logger.Info("updating existing cron", "id", cron.ID, "name", cron.Name)
			updated, err := d.gateway.Update(ctx, cron.ID, def)
			if err != nil {
				return "", "", fmt.Errorf("update cron %s: %w", cron.ID, err)
			}
			return "update", updated.ID, nil
		}
	}

	d.logger.Info("creating new cron", "name", def.Name)
	created, err := d.gateway.Create(ctx, def)
	if err != nil {
		return "", "", fmt.Errorf("create cron: %w", err)
	}
	return "create", created.ID, nil
}

func (d *Deployer) record(ctx context.Context, cronName string, status string, duration time.Duration, errText string, action string) {
	if d.ledger == nil {
		return
	}
	entry := ledger.Entry{
		Timestamp:  time.Now().UTC(),
		Cron:       cronName,
		Status:     status,
		DurationMS: duration.Milliseconds(),
		Error:      errText,
		Action:     action,
	}
	if err := d.ledger.Append(ctx, entry); err != nil {
		d.logger.Warn("failed to append deployment ledger entry", "cron", cronName, "error", err)
	}
}
