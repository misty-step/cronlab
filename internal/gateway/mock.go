package gateway

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/misty-step/cronlab/internal/definition"
)

type MockClient struct {
	ListFunc   func(ctx context.Context) ([]Cron, error)
	GetFunc    func(ctx context.Context, id string) (Cron, error)
	CreateFunc func(ctx context.Context, def definition.Definition) (Cron, error)
	UpdateFunc func(ctx context.Context, id string, def definition.Definition) (Cron, error)
	DeleteFunc func(ctx context.Context, id string) error
	RunFunc    func(ctx context.Context, id string) (RunResult, error)

	mu     sync.Mutex
	nextID int
	crons  map[string]Cron
}

func NewMockClient() *MockClient {
	return &MockClient{
		nextID: 1,
		crons:  make(map[string]Cron),
	}
}

func (m *MockClient) List(ctx context.Context) ([]Cron, error) {
	if m.ListFunc != nil {
		return m.ListFunc(ctx)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]Cron, 0, len(m.crons))
	for _, cron := range m.crons {
		result = append(result, cron)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result, nil
}

func (m *MockClient) Get(ctx context.Context, id string) (Cron, error) {
	if m.GetFunc != nil {
		return m.GetFunc(ctx, id)
	}
	if err := ctx.Err(); err != nil {
		return Cron{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	cron, ok := m.crons[id]
	if !ok {
		return Cron{}, ErrNotFound
	}
	return cron, nil
}

func (m *MockClient) Create(ctx context.Context, def definition.Definition) (Cron, error) {
	if m.CreateFunc != nil {
		return m.CreateFunc(ctx, def)
	}
	if err := ctx.Err(); err != nil {
		return Cron{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	id := fmt.Sprintf("cron-%d", m.nextID)
	m.nextID++
	cron := Cron{
		ID:            id,
		Name:          def.Name,
		Description:   def.Description,
		Schedule:      def.Schedule,
		Payload:       def.Payload,
		SessionTarget: def.SessionTarget,
		Delivery:      def.Delivery,
		Tags:          append([]string(nil), def.Tags...),
		Enabled:       true,
	}
	m.crons[id] = cron
	return cron, nil
}

func (m *MockClient) Update(ctx context.Context, id string, def definition.Definition) (Cron, error) {
	if m.UpdateFunc != nil {
		return m.UpdateFunc(ctx, id, def)
	}
	if err := ctx.Err(); err != nil {
		return Cron{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	cron, ok := m.crons[id]
	if !ok {
		return Cron{}, ErrNotFound
	}

	cron.Name = def.Name
	cron.Description = def.Description
	cron.Schedule = def.Schedule
	cron.Payload = def.Payload
	cron.SessionTarget = def.SessionTarget
	cron.Delivery = def.Delivery
	cron.Tags = append([]string(nil), def.Tags...)
	m.crons[id] = cron
	return cron, nil
}

func (m *MockClient) Delete(ctx context.Context, id string) error {
	if m.DeleteFunc != nil {
		return m.DeleteFunc(ctx, id)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.crons[id]; !ok {
		return ErrNotFound
	}
	delete(m.crons, id)
	return nil
}

func (m *MockClient) Run(ctx context.Context, id string) (RunResult, error) {
	if m.RunFunc != nil {
		return m.RunFunc(ctx, id)
	}
	if err := ctx.Err(); err != nil {
		return RunResult{}, err
	}

	m.mu.Lock()
	cron, ok := m.crons[id]
	if !ok {
		m.mu.Unlock()
		return RunResult{}, ErrNotFound
	}
	start := time.Now().UTC()
	end := start.Add(1200 * time.Millisecond)
	cron.LastRunStatus = "success"
	cron.LastRunAt = &end
	m.crons[id] = cron
	m.mu.Unlock()

	return RunResult{
		CronID:     id,
		Status:     "success",
		Output:     fmt.Sprintf("mock run for %s", cron.Name),
		Stdout:     fmt.Sprintf("mock run for %s", cron.Name),
		ExitCode:   0,
		DurationMS: 1200,
		StartedAt:  start,
		FinishedAt: end,
	}, nil
}
