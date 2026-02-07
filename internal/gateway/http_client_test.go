package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/misty-step/cronlab/internal/definition"
)

func TestHTTPClientCRUDAndRun(t *testing.T) {
	t.Parallel()

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/crons":
			_ = json.NewEncoder(w).Encode([]Cron{{ID: "c1", Name: "morning", Enabled: true}})
		case r.Method == http.MethodGet && r.URL.Path == "/crons/c1":
			_ = json.NewEncoder(w).Encode(Cron{ID: "c1", Name: "morning", Enabled: true})
		case r.Method == http.MethodPost && r.URL.Path == "/crons":
			var def definition.Definition
			if err := json.NewDecoder(r.Body).Decode(&def); err != nil {
				t.Fatalf("decode create body: %v", err)
			}
			_ = json.NewEncoder(w).Encode(Cron{ID: "c2", Name: def.Name, Enabled: true})
		case r.Method == http.MethodPut && r.URL.Path == "/crons/c2":
			var def definition.Definition
			if err := json.NewDecoder(r.Body).Decode(&def); err != nil {
				t.Fatalf("decode update body: %v", err)
			}
			_ = json.NewEncoder(w).Encode(Cron{ID: "c2", Name: def.Name, Enabled: true})
		case r.Method == http.MethodDelete && r.URL.Path == "/crons/c2":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/crons/c1/run":
			_ = json.NewEncoder(w).Encode(RunResult{CronID: "c1", Status: "success", ExitCode: 0, DurationMS: 1000, StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC()})
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
		}
	})
	server := httptest.NewServer(h)
	defer server.Close()

	client, err := NewHTTPClient(server.URL)
	if err != nil {
		t.Fatalf("NewHTTPClient() error = %v", err)
	}

	ctx := context.Background()
	list, err := client.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 1 || list[0].ID != "c1" {
		t.Fatalf("List() = %+v, want c1", list)
	}

	got, err := client.Get(ctx, "c1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Name != "morning" {
		t.Fatalf("Get() = %+v, want morning", got)
	}

	created, err := client.Create(ctx, definition.Definition{Name: "create-me"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.ID != "c2" || created.Name != "create-me" {
		t.Fatalf("Create() = %+v, want c2/create-me", created)
	}

	updated, err := client.Update(ctx, "c2", definition.Definition{Name: "updated"})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Name != "updated" {
		t.Fatalf("Update() = %+v, want updated", updated)
	}

	if err := client.Delete(ctx, "c2"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	run, err := client.Run(ctx, "c1")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if run.CronID != "c1" || run.Status != "success" {
		t.Fatalf("Run() = %+v, want success for c1", run)
	}
}

func TestHTTPClientErrors(t *testing.T) {
	t.Parallel()

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/crons/missing":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"missing"}`))
		case "/crons/bad":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"boom"}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("bad request"))
		}
	})
	server := httptest.NewServer(h)
	defer server.Close()

	client, err := NewHTTPClient(server.URL)
	if err != nil {
		t.Fatalf("NewHTTPClient() error = %v", err)
	}

	_, err = client.Get(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(missing) err = %v, want ErrNotFound", err)
	}

	_, err = client.Get(context.Background(), "bad")
	var httpErr HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("Get(bad) err = %v, want HTTPError", err)
	}
	if httpErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("HTTPError status = %d, want 500", httpErr.StatusCode)
	}
	if httpErr.Body != "boom" {
		t.Fatalf("HTTPError body = %q, want boom", httpErr.Body)
	}
}

func TestNewHTTPClientValidation(t *testing.T) {
	t.Parallel()

	if _, err := NewHTTPClient(""); err == nil {
		t.Fatalf("NewHTTPClient(empty) expected error")
	}
	if _, err := NewHTTPClient("localhost:1234"); err == nil {
		t.Fatalf("NewHTTPClient(localhost:1234) expected error")
	}
}

func TestHTTPClientContextCancel(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]Cron{})
	}))
	defer server.Close()

	client, err := NewHTTPClient(server.URL)
	if err != nil {
		t.Fatalf("NewHTTPClient() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.List(ctx); err == nil {
		t.Fatalf("List(canceled ctx) expected error")
	}
}

func TestHTTPClientOptionsAndHTTPErrorString(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]Cron{})
	}))
	defer server.Close()

	customHTTP := &http.Client{Timeout: 2 * time.Second}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	client, err := NewHTTPClient(server.URL, WithHTTPClient(customHTTP), WithLogger(logger))
	if err != nil {
		t.Fatalf("NewHTTPClient() error = %v", err)
	}
	if client.httpClient != customHTTP {
		t.Fatalf("http client option not applied")
	}
	if client.logger != logger {
		t.Fatalf("logger option not applied")
	}
	if _, err := client.List(context.Background()); err != nil {
		t.Fatalf("List() error = %v", err)
	}

	errStr := (HTTPError{StatusCode: 500, Body: "boom"}).Error()
	if errStr == "" || errStr == "gateway request failed with status 500" {
		t.Fatalf("HTTPError.Error() = %q, want message with body", errStr)
	}
}

func TestParseErrorMessageBranches(t *testing.T) {
	t.Parallel()

	if got := parseErrorMessage([]byte(`{"error":"boom"}`)); got != "boom" {
		t.Fatalf("parseErrorMessage(error) = %q", got)
	}
	if got := parseErrorMessage([]byte(`{"message":"oops"}`)); got != "oops" {
		t.Fatalf("parseErrorMessage(message) = %q", got)
	}
	if got := parseErrorMessage([]byte(`not-json`)); got != "" {
		t.Fatalf("parseErrorMessage(invalid) = %q, want empty", got)
	}
}
