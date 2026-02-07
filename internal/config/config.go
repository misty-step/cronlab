package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultGatewayURL = "http://localhost:4152"
	defaultModel      = "deepseek/deepseek-chat-v3-0324"
	defaultActivity   = "~/.cronlab/activity.jsonl"
	defaultRetention  = "90d"
)

type Config struct {
	Gateway struct {
		URL string `yaml:"url" json:"url"`
	} `yaml:"gateway" json:"gateway"`
	Reviewer struct {
		Model    string `yaml:"model" json:"model"`
		Fallback string `yaml:"fallback" json:"fallback,omitempty"`
	} `yaml:"reviewer" json:"reviewer"`
	Activity struct {
		Path      string `yaml:"path" json:"path"`
		Retention string `yaml:"retention" json:"retention"`
	} `yaml:"activity" json:"activity"`
	Alerts struct {
		Telegram  bool `yaml:"telegram" json:"telegram"`
		Threshold int  `yaml:"threshold" json:"threshold"`
	} `yaml:"alerts" json:"alerts"`
}

func Default() Config {
	var cfg Config
	cfg.Gateway.URL = defaultGatewayURL
	cfg.Reviewer.Model = defaultModel
	cfg.Activity.Path = defaultActivity
	cfg.Activity.Retention = defaultRetention
	cfg.Alerts.Threshold = 2
	return cfg
}

func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".cronlab/config.yaml"
	}
	return filepath.Join(home, ".cronlab", "config.yaml")
}

func Load(path string) (Config, error) {
	cfg := Default()

	if strings.TrimSpace(path) == "" {
		path = DefaultPath()
	}
	path = ExpandTilde(path)

	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			normalize(&cfg)
			return cfg, nil
		}
		return Config{}, fmt.Errorf("open config file: %w", err)
	}
	defer f.Close()

	loaded, err := decode(f)
	if err != nil {
		return Config{}, fmt.Errorf("decode config file: %w", err)
	}
	merge(&cfg, loaded)
	normalize(&cfg)
	return cfg, nil
}

func decode(r io.Reader) (Config, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func merge(dst *Config, src Config) {
	if strings.TrimSpace(src.Gateway.URL) != "" {
		dst.Gateway.URL = src.Gateway.URL
	}
	if strings.TrimSpace(src.Reviewer.Model) != "" {
		dst.Reviewer.Model = src.Reviewer.Model
	}
	if strings.TrimSpace(src.Reviewer.Fallback) != "" {
		dst.Reviewer.Fallback = src.Reviewer.Fallback
	}
	if strings.TrimSpace(src.Activity.Path) != "" {
		dst.Activity.Path = src.Activity.Path
	}
	if strings.TrimSpace(src.Activity.Retention) != "" {
		dst.Activity.Retention = src.Activity.Retention
	}
	dst.Alerts.Telegram = src.Alerts.Telegram
	if src.Alerts.Threshold > 0 {
		dst.Alerts.Threshold = src.Alerts.Threshold
	}
}

func normalize(cfg *Config) {
	cfg.Gateway.URL = strings.TrimSpace(cfg.Gateway.URL)
	if cfg.Gateway.URL == "" {
		cfg.Gateway.URL = defaultGatewayURL
	}
	cfg.Reviewer.Model = strings.TrimSpace(cfg.Reviewer.Model)
	if cfg.Reviewer.Model == "" {
		cfg.Reviewer.Model = defaultModel
	}
	cfg.Activity.Path = ExpandTilde(strings.TrimSpace(cfg.Activity.Path))
	if cfg.Activity.Path == "" {
		cfg.Activity.Path = ExpandTilde(defaultActivity)
	}
	cfg.Activity.Retention = strings.TrimSpace(cfg.Activity.Retention)
	if cfg.Activity.Retention == "" {
		cfg.Activity.Retention = defaultRetention
	}
	if cfg.Alerts.Threshold <= 0 {
		cfg.Alerts.Threshold = 2
	}
}

func ParseRetentionDuration(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New("retention is required")
	}
	if strings.HasSuffix(value, "d") {
		num := strings.TrimSpace(strings.TrimSuffix(value, "d"))
		days, err := strconv.Atoi(num)
		if err != nil || days < 0 {
			return 0, fmt.Errorf("invalid retention value %q", value)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	dur, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid retention value %q: %w", value, err)
	}
	return dur, nil
}

func ExpandTilde(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[2:])
}
