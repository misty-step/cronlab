package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Gateway.URL == "" || cfg.Reviewer.Model == "" || cfg.Activity.Path == "" {
		t.Fatalf("Load() defaults missing: %+v", cfg)
	}
}

func TestLoadOverridesValues(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	src := `
gateway:
  url: http://example-gateway:4152
reviewer:
  model: moonshot/kimi
  fallback: deepseek/fallback
activity:
  path: ~/custom/activity.jsonl
  retention: 30d
alerts:
  telegram: true
  threshold: 5
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Gateway.URL != "http://example-gateway:4152" {
		t.Fatalf("gateway URL = %q", cfg.Gateway.URL)
	}
	if cfg.Reviewer.Model != "moonshot/kimi" {
		t.Fatalf("reviewer model = %q", cfg.Reviewer.Model)
	}
	if cfg.Reviewer.Fallback != "deepseek/fallback" {
		t.Fatalf("reviewer fallback = %q", cfg.Reviewer.Fallback)
	}
	if !strings.Contains(cfg.Activity.Path, "custom") {
		t.Fatalf("activity path = %q, want expanded custom path", cfg.Activity.Path)
	}
	if cfg.Activity.Retention != "30d" {
		t.Fatalf("retention = %q", cfg.Activity.Retention)
	}
	if !cfg.Alerts.Telegram || cfg.Alerts.Threshold != 5 {
		t.Fatalf("alerts = %+v", cfg.Alerts)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	src := `
unknown: true
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatalf("Load() expected unknown field error")
	}
}

func TestParseRetentionDuration(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{in: "90d", want: 90 * 24 * time.Hour},
		{in: "12h", want: 12 * time.Hour},
		{in: "", wantErr: true},
		{in: "abc", wantErr: true},
	}

	for _, tc := range cases {
		got, err := ParseRetentionDuration(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("ParseRetentionDuration(%q) expected error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseRetentionDuration(%q) error = %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("ParseRetentionDuration(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestExpandTilde(t *testing.T) {
	t.Parallel()

	got := ExpandTilde("~/x/y")
	if strings.HasPrefix(got, "~/") {
		t.Fatalf("ExpandTilde did not expand: %s", got)
	}
}

func TestDefaultPath(t *testing.T) {
	t.Parallel()

	path := DefaultPath()
	if !strings.Contains(path, ".cronlab") {
		t.Fatalf("DefaultPath() = %s, want .cronlab", path)
	}
}
