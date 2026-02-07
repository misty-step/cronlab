package definition

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"gopkg.in/yaml.v3"
)

const (
	ScheduleKindCron     = "cron"
	ScheduleKindInterval = "interval"
	ScheduleKindOneShot  = "one-shot"

	PayloadKindSystemEvent = "systemEvent"
	PayloadKindAgentTurn   = "agentTurn"

	SessionTargetMain     = "main"
	SessionTargetIsolated = "isolated"
)

type Definition struct {
	Name          string     `yaml:"name" json:"name"`
	Description   string     `yaml:"description" json:"description"`
	Schedule      Schedule   `yaml:"schedule" json:"schedule"`
	Payload       Payload    `yaml:"payload" json:"payload"`
	SessionTarget string     `yaml:"sessionTarget" json:"sessionTarget"`
	Delivery      Delivery   `yaml:"delivery" json:"delivery"`
	Expected      Expected   `yaml:"expected" json:"expected"`
	Tags          []string   `yaml:"tags" json:"tags,omitempty"`
	Raw           *yaml.Node `yaml:"-" json:"-"`
}

type Schedule struct {
	Kind string `yaml:"kind" json:"kind"`
	Expr string `yaml:"expr" json:"expr"`
	TZ   string `yaml:"tz" json:"tz,omitempty"`
	At   string `yaml:"at" json:"at,omitempty"`
}

type Payload struct {
	Kind    string                 `yaml:"kind" json:"kind"`
	Event   string                 `yaml:"event" json:"event,omitempty"`
	Message string                 `yaml:"message" json:"message,omitempty"`
	Model   string                 `yaml:"model" json:"model,omitempty"`
	Data    map[string]interface{} `yaml:"data" json:"data,omitempty"`
}

type Delivery struct {
	Mode string `yaml:"mode" json:"mode,omitempty"`
}

type Expected struct {
	Description    string   `yaml:"description" json:"description,omitempty"`
	MaxDuration    Duration `yaml:"maxDuration" json:"maxDuration,omitempty"`
	MustContain    []string `yaml:"mustContain" json:"mustContain,omitempty"`
	MustNotContain []string `yaml:"mustNotContain" json:"mustNotContain,omitempty"`
}

type Duration struct {
	time.Duration
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf("%q", d.String())), nil
}

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return errors.New("duration must be a scalar string")
	}
	parsed, err := time.ParseDuration(strings.TrimSpace(node.Value))
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", node.Value, err)
	}
	d.Duration = parsed
	return nil
}

type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e ValidationError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return e.Field + ": " + e.Message
}

func ParseFile(path string) (*Definition, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open definition file: %w", err)
	}
	defer f.Close()
	return Parse(f)
}

func Parse(reader io.Reader) (*Definition, error) {
	dec := yaml.NewDecoder(reader)
	dec.KnownFields(true)

	var def Definition
	if err := dec.Decode(&def); err != nil {
		return nil, fmt.Errorf("parse yaml definition: %w", err)
	}
	return &def, nil
}

func Validate(def *Definition) []ValidationError {
	if def == nil {
		return []ValidationError{{Field: "definition", Message: "definition is required"}}
	}

	errs := make([]ValidationError, 0)
	appendErr := func(field string, msg string) {
		errs = append(errs, ValidationError{Field: field, Message: msg})
	}

	if strings.TrimSpace(def.Name) == "" {
		appendErr("name", "is required")
	}

	validateSchedule(def, appendErr)
	validatePayload(def, appendErr)
	validateSessionTarget(def, appendErr)
	validateDelivery(def, appendErr)
	validateExpected(def, appendErr)
	validateTags(def, appendErr)

	return errs
}

func validateSchedule(def *Definition, appendErr func(string, string)) {
	s := def.Schedule
	if strings.TrimSpace(s.Kind) == "" {
		appendErr("schedule.kind", "is required")
		return
	}

	switch s.Kind {
	case ScheduleKindCron:
		if strings.TrimSpace(s.Expr) == "" {
			appendErr("schedule.expr", "is required for cron schedules")
			return
		}
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		if _, err := parser.Parse(s.Expr); err != nil {
			appendErr("schedule.expr", "invalid cron expression")
		}
	case ScheduleKindInterval:
		if strings.TrimSpace(s.Expr) == "" {
			appendErr("schedule.expr", "is required for interval schedules")
			return
		}
		dur, err := time.ParseDuration(s.Expr)
		if err != nil {
			appendErr("schedule.expr", "invalid interval duration")
			return
		}
		if dur <= 0 {
			appendErr("schedule.expr", "interval must be greater than zero")
		}
	case ScheduleKindOneShot:
		target := strings.TrimSpace(s.At)
		field := "schedule.at"
		if target == "" {
			target = strings.TrimSpace(s.Expr)
			field = "schedule.expr"
		}
		if target == "" {
			appendErr("schedule.at", "is required for one-shot schedules")
			return
		}
		if _, err := time.Parse(time.RFC3339, target); err != nil {
			appendErr(field, "must be RFC3339 timestamp")
		}
	default:
		appendErr("schedule.kind", "must be one of: cron, interval, one-shot")
	}

	if tz := strings.TrimSpace(s.TZ); tz != "" {
		if _, err := time.LoadLocation(tz); err != nil {
			appendErr("schedule.tz", "must be a valid IANA timezone")
		}
	}
}

func validatePayload(def *Definition, appendErr func(string, string)) {
	p := def.Payload
	if strings.TrimSpace(p.Kind) == "" {
		appendErr("payload.kind", "is required")
		return
	}

	switch p.Kind {
	case PayloadKindSystemEvent:
		if strings.TrimSpace(p.Event) == "" {
			appendErr("payload.event", "is required for systemEvent payload")
		}
	case PayloadKindAgentTurn:
		if strings.TrimSpace(p.Message) == "" {
			appendErr("payload.message", "is required for agentTurn payload")
		}
	default:
		appendErr("payload.kind", "must be one of: systemEvent, agentTurn")
	}
}

func validateSessionTarget(def *Definition, appendErr func(string, string)) {
	target := strings.TrimSpace(def.SessionTarget)
	if target == "" {
		appendErr("sessionTarget", "is required")
		return
	}

	switch target {
	case SessionTargetMain:
		if def.Payload.Kind != PayloadKindSystemEvent {
			appendErr("sessionTarget", "main target requires payload.kind=systemEvent")
		}
	case SessionTargetIsolated:
		if def.Payload.Kind != PayloadKindAgentTurn {
			appendErr("sessionTarget", "isolated target requires payload.kind=agentTurn")
		}
	default:
		appendErr("sessionTarget", "must be one of: main, isolated")
	}
}

func validateDelivery(def *Definition, appendErr func(string, string)) {
	mode := strings.TrimSpace(def.Delivery.Mode)
	if mode == "" {
		return
	}
	if mode != "announce" && mode != "silent" {
		appendErr("delivery.mode", "must be one of: announce, silent")
	}
}

func validateExpected(def *Definition, appendErr func(string, string)) {
	if def.Expected.MaxDuration.Duration < 0 {
		appendErr("expected.maxDuration", "must be greater than or equal to zero")
	}
	for i, v := range def.Expected.MustContain {
		if strings.TrimSpace(v) == "" {
			appendErr(fmt.Sprintf("expected.mustContain[%d]", i), "must not be empty")
		}
	}
	for i, v := range def.Expected.MustNotContain {
		if strings.TrimSpace(v) == "" {
			appendErr(fmt.Sprintf("expected.mustNotContain[%d]", i), "must not be empty")
		}
	}
}

func validateTags(def *Definition, appendErr func(string, string)) {
	seen := map[string]struct{}{}
	for i, t := range def.Tags {
		t = strings.TrimSpace(t)
		if t == "" {
			appendErr(fmt.Sprintf("tags[%d]", i), "must not be empty")
			continue
		}
		if _, exists := seen[t]; exists {
			appendErr(fmt.Sprintf("tags[%d]", i), "duplicate tag")
			continue
		}
		seen[t] = struct{}{}
	}
}
