package ledger

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const defaultRelativePath = ".cronlab/activity.jsonl"

type Entry struct {
	Timestamp   time.Time `json:"ts"`
	Cron        string    `json:"cron"`
	Status      string    `json:"status"`
	DurationMS  int64     `json:"duration_ms,omitempty"`
	OutputBytes int64     `json:"output_bytes,omitempty"`
	Error       string    `json:"error,omitempty"`
	Action      string    `json:"action,omitempty"`
}

type QueryFilter struct {
	Cron   string
	Status string
	From   *time.Time
	To     *time.Time
	Limit  int
}

type Stats struct {
	Total           int            `json:"total"`
	SuccessCount    int            `json:"success_count"`
	FailureCount    int            `json:"failure_count"`
	SuccessRate     float64        `json:"success_rate"`
	AverageDuration float64        `json:"average_duration_ms"`
	FailurePatterns map[string]int `json:"failure_patterns"`
}

type Store struct {
	path string
	mu   sync.Mutex
}

func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return defaultRelativePath
	}
	return filepath.Join(home, defaultRelativePath)
}

func New(path string) *Store {
	if strings.TrimSpace(path) == "" {
		path = DefaultPath()
	}
	return &Store{path: expandTilde(path)}
}

func (s *Store) Path() string {
	return s.path
}

func (s *Store) Append(ctx context.Context, entry Entry) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if strings.TrimSpace(entry.Cron) == "" {
		return errors.New("entry cron is required")
	}
	if strings.TrimSpace(entry.Status) == "" {
		return errors.New("entry status is required")
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create ledger directory: %w", err)
	}

	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open ledger: %w", err)
	}
	defer f.Close()

	line, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal ledger entry: %w", err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("append ledger entry: %w", err)
	}
	return nil
}

func (s *Store) Query(ctx context.Context, filter QueryFilter) ([]Entry, error) {
	entries, err := s.scan(ctx, filter)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return entries, nil
}

func (s *Store) QueryStats(ctx context.Context, filter QueryFilter) (Stats, error) {
	entries, err := s.Query(ctx, filter)
	if err != nil {
		return Stats{}, err
	}
	return ComputeStats(entries), nil
}

func ComputeStats(entries []Entry) Stats {
	stats := Stats{FailurePatterns: make(map[string]int)}
	if len(entries) == 0 {
		return stats
	}

	var durationTotal int64
	var durationSamples int

	for _, entry := range entries {
		stats.Total++
		if strings.EqualFold(entry.Status, "success") {
			stats.SuccessCount++
		} else {
			stats.FailureCount++
			key := strings.TrimSpace(entry.Error)
			if key == "" {
				key = strings.TrimSpace(entry.Status)
				if key == "" {
					key = "unknown"
				}
			}
			stats.FailurePatterns[key]++
		}

		if entry.DurationMS > 0 {
			durationTotal += entry.DurationMS
			durationSamples++
		}
	}

	if stats.Total > 0 {
		stats.SuccessRate = float64(stats.SuccessCount) / float64(stats.Total)
	}
	if durationSamples > 0 {
		stats.AverageDuration = float64(durationTotal) / float64(durationSamples)
	}

	return stats
}

func (s *Store) scan(ctx context.Context, filter QueryFilter) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	f, err := os.Open(s.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	result := make([]Entry, 0)
	scanner := bufio.NewScanner(f)
	line := 0
	for scanner.Scan() {
		line++
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}

		var entry Entry
		if err := json.Unmarshal([]byte(text), &entry); err != nil {
			return nil, fmt.Errorf("parse ledger line %d: %w", line, err)
		}
		if !matches(entry, filter) {
			continue
		}
		result = append(result, entry)
		if filter.Limit > 0 && len(result) >= filter.Limit {
			break
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read ledger: %w", err)
	}

	return result, nil
}

func matches(entry Entry, filter QueryFilter) bool {
	if filter.Cron != "" && entry.Cron != filter.Cron {
		return false
	}
	if filter.Status != "" && !strings.EqualFold(entry.Status, filter.Status) {
		return false
	}
	if filter.From != nil && entry.Timestamp.Before(filter.From.UTC()) {
		return false
	}
	if filter.To != nil && entry.Timestamp.After(filter.To.UTC()) {
		return false
	}
	return true
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
