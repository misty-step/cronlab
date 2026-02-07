package gateway

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/misty-step/cronlab/internal/definition"
)

var ErrNotFound = errors.New("cron not found")

type GatewayClient interface {
	List(ctx context.Context) ([]Cron, error)
	Get(ctx context.Context, id string) (Cron, error)
	Create(ctx context.Context, def definition.Definition) (Cron, error)
	Update(ctx context.Context, id string, def definition.Definition) (Cron, error)
	Delete(ctx context.Context, id string) error
	Run(ctx context.Context, id string) (RunResult, error)
}

type Cron struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	Description   string              `json:"description,omitempty"`
	Schedule      definition.Schedule `json:"schedule"`
	Payload       definition.Payload  `json:"payload"`
	SessionTarget string              `json:"sessionTarget"`
	Delivery      definition.Delivery `json:"delivery,omitempty"`
	Tags          []string            `json:"tags,omitempty"`
	Enabled       bool                `json:"enabled"`
	LastRunStatus string              `json:"lastRunStatus,omitempty"`
	LastRunAt     *time.Time          `json:"lastRunAt,omitempty"`
	FailureCount  int                 `json:"failureCount,omitempty"`
}

type RunResult struct {
	CronID     string    `json:"cronId"`
	Status     string    `json:"status"`
	Output     string    `json:"output,omitempty"`
	Error      string    `json:"error,omitempty"`
	ExitCode   int       `json:"exitCode"`
	DurationMS int64     `json:"duration_ms"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
}

type HTTPError struct {
	StatusCode int
	Body       string
}

func (e HTTPError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("gateway request failed with status %d", e.StatusCode)
	}
	return fmt.Sprintf("gateway request failed with status %d: %s", e.StatusCode, e.Body)
}
