package gateway

import (
	"context"
	"errors"
	"testing"

	"github.com/misty-step/cronlab/internal/definition"
)

func TestMockClientCRUDAndRun(t *testing.T) {
	t.Parallel()

	mock := NewMockClient()
	ctx := context.Background()

	created, err := mock.Create(ctx, definition.Definition{
		Name:          "morning-brief",
		Schedule:      definition.Schedule{Kind: definition.ScheduleKindCron, Expr: "0 9 * * *"},
		Payload:       definition.Payload{Kind: definition.PayloadKindAgentTurn, Message: "go"},
		SessionTarget: definition.SessionTargetIsolated,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.ID == "" {
		t.Fatalf("Create() ID is empty")
	}

	list, err := mock.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List() len = %d, want 1", len(list))
	}

	got, err := mock.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Name != "morning-brief" {
		t.Fatalf("Get() = %+v, want morning-brief", got)
	}

	updated, err := mock.Update(ctx, created.ID, definition.Definition{Name: "updated"})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Name != "updated" {
		t.Fatalf("Update() = %+v, want updated", updated)
	}

	run, err := mock.Run(ctx, created.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if run.Status != "success" {
		t.Fatalf("Run() = %+v, want success", run)
	}

	if err := mock.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if _, err := mock.Get(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(deleted) err = %v, want ErrNotFound", err)
	}
}

func TestMockClientOverrides(t *testing.T) {
	t.Parallel()

	mock := NewMockClient()
	expectedErr := errors.New("forced")
	mock.ListFunc = func(ctx context.Context) ([]Cron, error) {
		return nil, expectedErr
	}

	_, err := mock.List(context.Background())
	if !errors.Is(err, expectedErr) {
		t.Fatalf("List() err = %v, want forced", err)
	}
}

func TestMockClientContextCancel(t *testing.T) {
	t.Parallel()

	mock := NewMockClient()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := mock.List(ctx); err == nil {
		t.Fatalf("List(canceled) expected error")
	}
	if _, err := mock.Create(ctx, definition.Definition{Name: "x"}); err == nil {
		t.Fatalf("Create(canceled) expected error")
	}
	if err := mock.Delete(ctx, "cron-1"); err == nil {
		t.Fatalf("Delete(canceled) expected error")
	}
}
