package deployer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/misty-step/cronlab/internal/definition"
	"github.com/misty-step/cronlab/internal/gateway"
	"github.com/misty-step/cronlab/internal/ledger"
	"github.com/misty-step/cronlab/internal/reviewer"
	"github.com/misty-step/cronlab/internal/tester"
)

type stubTester struct {
	report tester.Report
	err    error
	called int
	last   tester.Options
}

func (s *stubTester) Run(ctx context.Context, def definition.Definition, opts tester.Options) (tester.Report, error) {
	s.called++
	s.last = opts
	return s.report, s.err
}

type stubReviewer struct {
	result reviewer.Result
	err    error
	called int
	last   reviewer.Options
}

func (s *stubReviewer) Review(ctx context.Context, report tester.Report, opts reviewer.Options) (reviewer.Result, error) {
	s.called++
	s.last = opts
	return s.result, s.err
}

type captureLedger struct {
	entries []ledger.Entry
}

func (l *captureLedger) Append(ctx context.Context, entry ledger.Entry) error {
	l.entries = append(l.entries, entry)
	return nil
}

func validDef() definition.Definition {
	return definition.Definition{
		Name:          "morning-brief",
		Schedule:      definition.Schedule{Kind: definition.ScheduleKindCron, Expr: "0 9 * * *"},
		Payload:       definition.Payload{Kind: definition.PayloadKindAgentTurn, Message: "Generate and send morning brief"},
		SessionTarget: definition.SessionTargetIsolated,
	}
}

func TestDeployValidationGate(t *testing.T) {
	t.Parallel()

	gw := gateway.NewMockClient()
	tst := &stubTester{}
	rev := &stubReviewer{}
	act := &captureLedger{}
	d := New(gw, tst, rev, act, nil)

	_, err := d.Deploy(context.Background(), definition.Definition{Name: "bad"}, Options{})
	if err == nil {
		t.Fatalf("Deploy() expected validation gate error")
	}
	var gateErr GateError
	if !errors.As(err, &gateErr) || gateErr.Stage != StageValidate {
		t.Fatalf("Deploy() err = %v, want StageValidate GateError", err)
	}
	if tst.called != 0 || rev.called != 0 {
		t.Fatalf("tester/reviewer should not run on validation failure")
	}
	if len(act.entries) != 1 || act.entries[0].Action != "deploy_validate_failed" {
		t.Fatalf("ledger entries = %+v, want deploy_validate_failed", act.entries)
	}
}

func TestDeployTestGateFail(t *testing.T) {
	t.Parallel()

	gw := gateway.NewMockClient()
	tst := &stubTester{report: tester.Report{Passed: false, Status: tester.ReportStatusFail}}
	rev := &stubReviewer{}
	act := &captureLedger{}
	d := New(gw, tst, rev, act, nil)

	_, err := d.Deploy(context.Background(), validDef(), Options{})
	if err == nil {
		t.Fatalf("Deploy() expected test gate error")
	}
	var gateErr GateError
	if !errors.As(err, &gateErr) || gateErr.Stage != StageTest {
		t.Fatalf("Deploy() err = %v, want StageTest GateError", err)
	}
	if rev.called != 0 {
		t.Fatalf("reviewer should not run when test fails")
	}
	if len(act.entries) != 1 || act.entries[0].Action != "deploy_test_failed" {
		t.Fatalf("ledger entries = %+v, want deploy_test_failed", act.entries)
	}
}

func TestDeployReviewGateFail(t *testing.T) {
	t.Parallel()

	gw := gateway.NewMockClient()
	tst := &stubTester{report: tester.Report{Passed: true, Status: tester.ReportStatusPass}}
	rev := &stubReviewer{result: reviewer.Result{Passed: false, Status: "FAIL"}}
	act := &captureLedger{}
	d := New(gw, tst, rev, act, nil)

	_, err := d.Deploy(context.Background(), validDef(), Options{})
	if err == nil {
		t.Fatalf("Deploy() expected review gate error")
	}
	var gateErr GateError
	if !errors.As(err, &gateErr) || gateErr.Stage != StageReview {
		t.Fatalf("Deploy() err = %v, want StageReview GateError", err)
	}
	if len(act.entries) != 1 || act.entries[0].Action != "deploy_review_failed" {
		t.Fatalf("ledger entries = %+v, want deploy_review_failed", act.entries)
	}
}

func TestDeployDryRun(t *testing.T) {
	t.Parallel()

	gw := gateway.NewMockClient()
	listCalled := false
	gw.ListFunc = func(ctx context.Context) ([]gateway.Cron, error) {
		listCalled = true
		return nil, nil
	}
	tst := &stubTester{report: tester.Report{Passed: true, Status: tester.ReportStatusPass}}
	rev := &stubReviewer{result: reviewer.Result{Passed: true, Status: "PASS"}}
	act := &captureLedger{}
	d := New(gw, tst, rev, act, nil)

	res, err := d.Deploy(context.Background(), validDef(), Options{DryRun: true, Timeout: 2 * time.Minute, Model: "model-x"})
	if err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
	if res.Action != "dry-run" || !res.DryRun {
		t.Fatalf("result = %+v, want dry-run action", res)
	}
	if listCalled {
		t.Fatalf("gateway list should not be called in dry-run")
	}
	if tst.last.Timeout != 2*time.Minute {
		t.Fatalf("tester timeout = %v, want 2m", tst.last.Timeout)
	}
	if rev.last.Model != "model-x" {
		t.Fatalf("reviewer model = %q, want model-x", rev.last.Model)
	}
	if len(act.entries) != 1 || act.entries[0].Action != "deploy_dry_run" || act.entries[0].Status != "success" {
		t.Fatalf("ledger entries = %+v, want deploy_dry_run success", act.entries)
	}
}

func TestDeployCreatePath(t *testing.T) {
	t.Parallel()

	gw := gateway.NewMockClient()
	gw.ListFunc = func(ctx context.Context) ([]gateway.Cron, error) { return nil, nil }
	gw.CreateFunc = func(ctx context.Context, def definition.Definition) (gateway.Cron, error) {
		return gateway.Cron{ID: "cron-101", Name: def.Name}, nil
	}
	tst := &stubTester{report: tester.Report{Passed: true, Status: tester.ReportStatusPass}}
	rev := &stubReviewer{result: reviewer.Result{Passed: true, Status: "PASS"}}
	act := &captureLedger{}
	d := New(gw, tst, rev, act, nil)

	res, err := d.Deploy(context.Background(), validDef(), Options{})
	if err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
	if res.Action != "create" || res.CronID != "cron-101" {
		t.Fatalf("result = %+v, want create cron-101", res)
	}
	if len(act.entries) != 1 || act.entries[0].Action != "deploy_create" || act.entries[0].Status != "success" {
		t.Fatalf("ledger entries = %+v, want deploy_create success", act.entries)
	}
}

func TestDeployUpdatePath(t *testing.T) {
	t.Parallel()

	gw := gateway.NewMockClient()
	updated := false
	gw.ListFunc = func(ctx context.Context) ([]gateway.Cron, error) {
		return []gateway.Cron{{ID: "cron-42", Name: "morning-brief"}}, nil
	}
	gw.UpdateFunc = func(ctx context.Context, id string, def definition.Definition) (gateway.Cron, error) {
		updated = true
		return gateway.Cron{ID: id, Name: def.Name}, nil
	}
	tst := &stubTester{report: tester.Report{Passed: true, Status: tester.ReportStatusPass}}
	rev := &stubReviewer{result: reviewer.Result{Passed: true, Status: "PASS"}}
	act := &captureLedger{}
	d := New(gw, tst, rev, act, nil)

	res, err := d.Deploy(context.Background(), validDef(), Options{})
	if err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
	if !updated {
		t.Fatalf("expected update to be called")
	}
	if res.Action != "update" || res.CronID != "cron-42" {
		t.Fatalf("result = %+v, want update cron-42", res)
	}
	if len(act.entries) != 1 || act.entries[0].Action != "deploy_update" || act.entries[0].Status != "success" {
		t.Fatalf("ledger entries = %+v, want deploy_update success", act.entries)
	}
}

func TestDeployApplyError(t *testing.T) {
	t.Parallel()

	gw := gateway.NewMockClient()
	gw.ListFunc = func(ctx context.Context) ([]gateway.Cron, error) {
		return nil, errors.New("gateway offline")
	}
	tst := &stubTester{report: tester.Report{Passed: true, Status: tester.ReportStatusPass}}
	rev := &stubReviewer{result: reviewer.Result{Passed: true, Status: "PASS"}}
	act := &captureLedger{}
	d := New(gw, tst, rev, act, nil)

	_, err := d.Deploy(context.Background(), validDef(), Options{})
	if err == nil {
		t.Fatalf("Deploy() expected apply error")
	}
	var gateErr GateError
	if !errors.As(err, &gateErr) || gateErr.Stage != StageDeploy {
		t.Fatalf("Deploy() err = %v, want StageDeploy GateError", err)
	}
	if len(act.entries) != 1 || act.entries[0].Action != "deploy_apply_failed" {
		t.Fatalf("ledger entries = %+v, want deploy_apply_failed", act.entries)
	}
}
