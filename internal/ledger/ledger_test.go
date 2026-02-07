package ledger

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppendAndQuery(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := New(filepath.Join(dir, "activity.jsonl"))
	ctx := context.Background()

	now := time.Date(2026, 2, 7, 15, 0, 0, 0, time.UTC)
	entries := []Entry{
		{Timestamp: now, Cron: "morning-brief", Status: "success", DurationMS: 4500, OutputBytes: 1234},
		{Timestamp: now.Add(5 * time.Minute), Cron: "conscience", Status: "error", DurationMS: 30000, Error: "timeout exceeded"},
		{Timestamp: now.Add(10 * time.Minute), Cron: "morning-brief", Status: "success", DurationMS: 3200},
	}

	for _, entry := range entries {
		if err := store.Append(ctx, entry); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}

	got, err := store.Query(ctx, QueryFilter{Cron: "morning-brief"})
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Query() len = %d, want 2", len(got))
	}

	from := now.Add(3 * time.Minute)
	to := now.Add(7 * time.Minute)
	got, err = store.Query(ctx, QueryFilter{From: &from, To: &to})
	if err != nil {
		t.Fatalf("Query() range error = %v", err)
	}
	if len(got) != 1 || got[0].Cron != "conscience" {
		t.Fatalf("Query() range = %+v, want conscience row", got)
	}

	got, err = store.Query(ctx, QueryFilter{Status: "SUCCESS", Limit: 1})
	if err != nil {
		t.Fatalf("Query() status error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Query() limit len = %d, want 1", len(got))
	}
}

func TestAppendValidation(t *testing.T) {
	t.Parallel()

	store := New(filepath.Join(t.TempDir(), "activity.jsonl"))
	ctx := context.Background()

	if err := store.Append(ctx, Entry{Status: "success"}); err == nil {
		t.Fatalf("Append() expected cron validation error")
	}
	if err := store.Append(ctx, Entry{Cron: "cron"}); err == nil {
		t.Fatalf("Append() expected status validation error")
	}
}

func TestQueryMissingFileReturnsEmpty(t *testing.T) {
	t.Parallel()

	store := New(filepath.Join(t.TempDir(), "nope.jsonl"))
	got, err := store.Query(context.Background(), QueryFilter{})
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Query() len = %d, want 0", len(got))
	}
}

func TestQueryReturnsParseErrorForBadJSONLine(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "activity.jsonl")
	if err := os.WriteFile(path, []byte("{\"ts\":\"2026-02-07T15:00:00Z\",\"cron\":\"ok\",\"status\":\"success\"}\nnot-json\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	store := New(path)
	_, err := store.Query(context.Background(), QueryFilter{})
	if err == nil {
		t.Fatalf("Query() expected parse error")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("Query() error = %v, want line number", err)
	}
}

func TestComputeStats(t *testing.T) {
	t.Parallel()

	entries := []Entry{
		{Cron: "a", Status: "success", DurationMS: 100},
		{Cron: "b", Status: "error", DurationMS: 300, Error: "timeout"},
		{Cron: "c", Status: "failed", DurationMS: 500, Error: "timeout"},
		{Cron: "d", Status: "success"},
	}

	stats := ComputeStats(entries)
	if stats.Total != 4 {
		t.Fatalf("Total = %d, want 4", stats.Total)
	}
	if stats.SuccessCount != 2 {
		t.Fatalf("SuccessCount = %d, want 2", stats.SuccessCount)
	}
	if stats.FailureCount != 2 {
		t.Fatalf("FailureCount = %d, want 2", stats.FailureCount)
	}
	if stats.SuccessRate != 0.5 {
		t.Fatalf("SuccessRate = %v, want 0.5", stats.SuccessRate)
	}
	if stats.AverageDuration != 300 {
		t.Fatalf("AverageDuration = %v, want 300", stats.AverageDuration)
	}
	if stats.FailurePatterns["timeout"] != 2 {
		t.Fatalf("FailurePatterns = %+v, want timeout=2", stats.FailurePatterns)
	}
}

func TestQueryStats(t *testing.T) {
	t.Parallel()

	store := New(filepath.Join(t.TempDir(), "activity.jsonl"))
	ctx := context.Background()

	for _, entry := range []Entry{
		{Cron: "a", Status: "success", DurationMS: 10},
		{Cron: "a", Status: "error", DurationMS: 20, Error: "boom"},
		{Cron: "b", Status: "success", DurationMS: 30},
	} {
		if err := store.Append(ctx, entry); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}

	stats, err := store.QueryStats(ctx, QueryFilter{Cron: "a"})
	if err != nil {
		t.Fatalf("QueryStats() error = %v", err)
	}
	if stats.Total != 2 || stats.SuccessCount != 1 || stats.FailureCount != 1 {
		t.Fatalf("QueryStats() = %+v, want total=2 success=1 failure=1", stats)
	}
}

func TestContextCancel(t *testing.T) {
	t.Parallel()

	store := New(filepath.Join(t.TempDir(), "activity.jsonl"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := store.Append(ctx, Entry{Cron: "a", Status: "success"}); err == nil {
		t.Fatalf("Append() expected context cancellation error")
	}
	if _, err := store.Query(ctx, QueryFilter{}); err == nil {
		t.Fatalf("Query() expected context cancellation error")
	}
}

func TestDefaultPath(t *testing.T) {
	t.Parallel()

	path := DefaultPath()
	if !strings.Contains(path, ".cronlab") {
		t.Fatalf("DefaultPath() = %s, want .cronlab segment", path)
	}
}
