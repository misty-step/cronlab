package definition

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseAndValidateValidAgentTurnDefinition(t *testing.T) {
	src := `
name: morning-brief
description: "Daily morning briefing"
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
  description: "Should produce a morning brief message"
  maxDuration: 120s
  mustContain: ["morning", "brief"]
  mustNotContain: ["error", "failed"]
tags:
  - daily
  - briefing
`
	def, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if got := Validate(def); len(got) != 0 {
		t.Fatalf("Validate() errors = %v, want none", got)
	}
}

func TestParseAndValidateValidSystemEventDefinition(t *testing.T) {
	src := `
name: sweep-stale-state
schedule:
  kind: interval
  expr: "10m"
payload:
  kind: systemEvent
  event: stale_state_sweep
sessionTarget: main
`
	def, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if got := Validate(def); len(got) != 0 {
		t.Fatalf("Validate() errors = %v, want none", got)
	}
}

func TestParseRejectsUnknownField(t *testing.T) {
	src := `
name: bad
unexpected: true
schedule:
  kind: cron
  expr: "* * * * *"
payload:
  kind: systemEvent
  event: foo
sessionTarget: main
`
	_, err := Parse(strings.NewReader(src))
	if err == nil {
		t.Fatalf("Parse() expected error, got nil")
	}
	if !strings.Contains(err.Error(), "field unexpected not found") {
		t.Fatalf("Parse() error = %v, want unknown field", err)
	}
}

func TestValidateNilDefinition(t *testing.T) {
	errs := Validate(nil)
	if len(errs) != 1 {
		t.Fatalf("Validate(nil) len=%d, want 1", len(errs))
	}
	if errs[0].Field != "definition" {
		t.Fatalf("Validate(nil) field=%q, want definition", errs[0].Field)
	}
}

func TestValidateScheduleRules(t *testing.T) {
	tests := []struct {
		name string
		def  Definition
		want string
	}{
		{
			name: "invalid cron",
			def: Definition{
				Name:          "x",
				Schedule:      Schedule{Kind: ScheduleKindCron, Expr: "not cron"},
				Payload:       Payload{Kind: PayloadKindSystemEvent, Event: "evt"},
				SessionTarget: SessionTargetMain,
			},
			want: "schedule.expr",
		},
		{
			name: "invalid interval",
			def: Definition{
				Name:          "x",
				Schedule:      Schedule{Kind: ScheduleKindInterval, Expr: "nope"},
				Payload:       Payload{Kind: PayloadKindSystemEvent, Event: "evt"},
				SessionTarget: SessionTargetMain,
			},
			want: "schedule.expr",
		},
		{
			name: "zero interval",
			def: Definition{
				Name:          "x",
				Schedule:      Schedule{Kind: ScheduleKindInterval, Expr: "0s"},
				Payload:       Payload{Kind: PayloadKindSystemEvent, Event: "evt"},
				SessionTarget: SessionTargetMain,
			},
			want: "schedule.expr",
		},
		{
			name: "one-shot requires timestamp",
			def: Definition{
				Name:          "x",
				Schedule:      Schedule{Kind: ScheduleKindOneShot},
				Payload:       Payload{Kind: PayloadKindSystemEvent, Event: "evt"},
				SessionTarget: SessionTargetMain,
			},
			want: "schedule.at",
		},
		{
			name: "one-shot bad timestamp",
			def: Definition{
				Name:          "x",
				Schedule:      Schedule{Kind: ScheduleKindOneShot, At: "tomorrow"},
				Payload:       Payload{Kind: PayloadKindSystemEvent, Event: "evt"},
				SessionTarget: SessionTargetMain,
			},
			want: "schedule.at",
		},
		{
			name: "invalid timezone",
			def: Definition{
				Name:          "x",
				Schedule:      Schedule{Kind: ScheduleKindCron, Expr: "0 * * * *", TZ: "Mars/Olympus"},
				Payload:       Payload{Kind: PayloadKindSystemEvent, Event: "evt"},
				SessionTarget: SessionTargetMain,
			},
			want: "schedule.tz",
		},
		{
			name: "unknown schedule kind",
			def: Definition{
				Name:          "x",
				Schedule:      Schedule{Kind: "weekly"},
				Payload:       Payload{Kind: PayloadKindSystemEvent, Event: "evt"},
				SessionTarget: SessionTargetMain,
			},
			want: "schedule.kind",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := Validate(&tt.def)
			if !containsField(errs, tt.want) {
				t.Fatalf("Validate() errors=%v, want field %q", errs, tt.want)
			}
		})
	}
}

func TestValidatePayloadAndSessionRules(t *testing.T) {
	def := &Definition{
		Name:          "x",
		Schedule:      Schedule{Kind: ScheduleKindCron, Expr: "0 * * * *"},
		Payload:       Payload{Kind: PayloadKindAgentTurn, Message: "ok"},
		SessionTarget: SessionTargetMain,
	}
	errs := Validate(def)
	if !containsField(errs, "sessionTarget") {
		t.Fatalf("Validate() errors=%v, want sessionTarget mismatch", errs)
	}

	def = &Definition{
		Name:          "x",
		Schedule:      Schedule{Kind: ScheduleKindCron, Expr: "0 * * * *"},
		Payload:       Payload{Kind: PayloadKindSystemEvent},
		SessionTarget: SessionTargetMain,
	}
	errs = Validate(def)
	if !containsField(errs, "payload.event") {
		t.Fatalf("Validate() errors=%v, want payload.event required", errs)
	}

	def = &Definition{
		Name:          "x",
		Schedule:      Schedule{Kind: ScheduleKindCron, Expr: "0 * * * *"},
		Payload:       Payload{Kind: "unsupported"},
		SessionTarget: SessionTargetMain,
	}
	errs = Validate(def)
	if !containsField(errs, "payload.kind") {
		t.Fatalf("Validate() errors=%v, want payload.kind", errs)
	}
}

func TestValidateExpectedAndTags(t *testing.T) {
	def := &Definition{
		Name:          "x",
		Schedule:      Schedule{Kind: ScheduleKindCron, Expr: "0 * * * *"},
		Payload:       Payload{Kind: PayloadKindSystemEvent, Event: "evt"},
		SessionTarget: SessionTargetMain,
		Expected: Expected{
			MustContain:    []string{"ok", ""},
			MustNotContain: []string{"", "bad"},
		},
		Tags: []string{"daily", "", "daily"},
	}
	errs := Validate(def)

	for _, field := range []string{"expected.mustContain[1]", "expected.mustNotContain[0]", "tags[1]", "tags[2]"} {
		if !containsField(errs, field) {
			t.Fatalf("Validate() errors=%v, want %s", errs, field)
		}
	}
}

func TestDurationUnmarshalError(t *testing.T) {
	src := `
name: bad-duration
schedule:
  kind: cron
  expr: "0 * * * *"
payload:
  kind: systemEvent
  event: evt
sessionTarget: main
expected:
  maxDuration: no-duration
`
	_, err := Parse(strings.NewReader(src))
	if err == nil {
		t.Fatalf("Parse() expected duration parse error")
	}
	if !strings.Contains(err.Error(), "invalid duration") {
		t.Fatalf("Parse() error = %v, want invalid duration", err)
	}
}

func TestOneShotExprFallback(t *testing.T) {
	def := &Definition{
		Name:          "x",
		Schedule:      Schedule{Kind: ScheduleKindOneShot, Expr: "2026-02-07T15:00:00Z"},
		Payload:       Payload{Kind: PayloadKindSystemEvent, Event: "evt"},
		SessionTarget: SessionTargetMain,
	}
	errs := Validate(def)
	if len(errs) != 0 {
		t.Fatalf("Validate() errors=%v, want none", errs)
	}
}

func TestParseFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "definition.yaml")
	src := `
name: file-parse
schedule:
  kind: cron
  expr: "0 * * * *"
payload:
  kind: systemEvent
  event: evt
sessionTarget: main
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	def, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}
	if def.Name != "file-parse" {
		t.Fatalf("ParseFile() name = %q, want file-parse", def.Name)
	}
}

func TestValidationErrorAndDurationJSON(t *testing.T) {
	t.Parallel()

	v := ValidationError{Field: "field", Message: "bad"}
	if got := v.Error(); got != "field: bad" {
		t.Fatalf("ValidationError.Error() = %q, want field: bad", got)
	}
	if got := (ValidationError{Message: "bad"}).Error(); got != "bad" {
		t.Fatalf("ValidationError.Error(no-field) = %q, want bad", got)
	}

	var dur Duration
	if !dur.IsZero() {
		t.Fatalf("Duration.IsZero() expected true for zero duration")
	}
	if err := dur.UnmarshalJSON([]byte(`"15s"`)); err != nil {
		t.Fatalf("Duration.UnmarshalJSON() error = %v", err)
	}
	if dur.IsZero() || dur.Duration != 15*time.Second {
		t.Fatalf("Duration after unmarshal = %v, want 15s", dur.Duration)
	}
	encoded, err := dur.MarshalJSON()
	if err != nil {
		t.Fatalf("Duration.MarshalJSON() error = %v", err)
	}
	if string(encoded) != `"15s"` {
		t.Fatalf("Duration.MarshalJSON() = %s, want \"15s\"", string(encoded))
	}
	if err := dur.UnmarshalJSON([]byte(`123`)); err == nil {
		t.Fatalf("Duration.UnmarshalJSON(non-string) expected error")
	}
}

func TestValidateDeliveryAndSessionTargetBranches(t *testing.T) {
	t.Parallel()

	def := &Definition{
		Name:          "x",
		Schedule:      Schedule{Kind: ScheduleKindCron, Expr: "0 * * * *"},
		Payload:       Payload{Kind: PayloadKindSystemEvent, Event: "evt"},
		SessionTarget: "unsupported",
		Delivery:      Delivery{Mode: "loud"},
	}
	errs := Validate(def)
	if !containsField(errs, "sessionTarget") || !containsField(errs, "delivery.mode") {
		t.Fatalf("Validate() errs=%v, want sessionTarget and delivery.mode", errs)
	}

	def = &Definition{
		Name:          "x",
		Schedule:      Schedule{Kind: ScheduleKindCron, Expr: "0 * * * *"},
		Payload:       Payload{Kind: PayloadKindSystemEvent, Event: "evt"},
		SessionTarget: SessionTargetMain,
		Expected:      Expected{MaxDuration: Duration{Duration: -time.Second}},
	}
	errs = Validate(def)
	if !containsField(errs, "expected.maxDuration") {
		t.Fatalf("Validate() errs=%v, want expected.maxDuration", errs)
	}
}

func containsField(errs []ValidationError, field string) bool {
	for _, err := range errs {
		if err.Field == field {
			return true
		}
	}
	return false
}
