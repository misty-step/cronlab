package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/misty-step/cronlab/internal/ledger"
	"github.com/misty-step/cronlab/internal/reviewer"
	"github.com/misty-step/cronlab/internal/tester"
)

type fakeGateway struct {
	mu    sync.Mutex
	next  int
	crons map[string]map[string]any
}

func newFakeGatewayServer() *httptest.Server {
	state := &fakeGateway{next: 1, crons: make(map[string]map[string]any)}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := strings.Trim(r.URL.Path, "/")
		parts := strings.Split(path, "/")
		if path == "crons" && r.Method == http.MethodGet {
			state.mu.Lock()
			list := make([]map[string]any, 0, len(state.crons))
			for _, cron := range state.crons {
				list = append(list, cron)
			}
			state.mu.Unlock()
			_ = json.NewEncoder(w).Encode(list)
			return
		}
		if path == "crons" && r.Method == http.MethodPost {
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"bad json"}`))
				return
			}

			state.mu.Lock()
			id := fmt.Sprintf("c%d", state.next)
			state.next++
			cron := map[string]any{
				"id":            id,
				"name":          payload["name"],
				"schedule":      payload["schedule"],
				"payload":       payload["payload"],
				"sessionTarget": payload["sessionTarget"],
				"enabled":       true,
			}
			state.crons[id] = cron
			state.mu.Unlock()
			_ = json.NewEncoder(w).Encode(cron)
			return
		}

		if len(parts) == 2 && parts[0] == "crons" && r.Method == http.MethodGet {
			state.mu.Lock()
			cron, ok := state.crons[parts[1]]
			state.mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":"not found"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(cron)
			return
		}

		if len(parts) == 2 && parts[0] == "crons" && r.Method == http.MethodPut {
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			state.mu.Lock()
			cron, ok := state.crons[parts[1]]
			if !ok {
				state.mu.Unlock()
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":"not found"}`))
				return
			}
			cron["name"] = payload["name"]
			cron["schedule"] = payload["schedule"]
			cron["payload"] = payload["payload"]
			cron["sessionTarget"] = payload["sessionTarget"]
			state.crons[parts[1]] = cron
			state.mu.Unlock()
			_ = json.NewEncoder(w).Encode(cron)
			return
		}

		if len(parts) == 2 && parts[0] == "crons" && r.Method == http.MethodDelete {
			state.mu.Lock()
			delete(state.crons, parts[1])
			state.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if len(parts) == 3 && parts[0] == "crons" && parts[2] == "run" && r.Method == http.MethodPost {
			now := time.Now().UTC()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"cronId":      parts[1],
				"status":      "success",
				"stdout":      "Morning brief generated for today",
				"exitCode":    0,
				"duration_ms": 1000,
				"startedAt":   now,
				"finishedAt":  now,
			})
			return
		}

		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"no route"}`))
	}))
}

func newFakeReviewerServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"content": `{"status":"PASS","commentary":"Looks good","issues":[]}`,
				},
			}},
		})
	}))
}

func writeConfig(t *testing.T, gatewayURL string, activityPath string) string {
	t.Helper()
	content := fmt.Sprintf(`
gateway:
  url: %s
reviewer:
  model: deepseek/deepseek-chat-v3-0324
activity:
  path: %s
  retention: 90d
`, gatewayURL, activityPath)

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	return path
}

func writeValidDefinition(t *testing.T) string {
	t.Helper()
	content := `
name: morning-brief
description: Daily morning briefing
schedule:
  kind: cron
  expr: "0 9 * * *"
  tz: America/Chicago
payload:
  kind: agentTurn
  message: Generate and send the morning brief
  model: openrouter/moonshotai/kimi-k2.5
sessionTarget: isolated
delivery:
  mode: announce
expected:
  description: Should produce morning brief
  maxDuration: 120s
  mustContain: ["morning", "brief"]
  mustNotContain: ["error", "failed"]
tags: [daily, briefing]
`
	path := filepath.Join(t.TempDir(), "definition.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(definition) error = %v", err)
	}
	return path
}

func executeCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newRootCmd()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return stdout.String() + stderr.String(), err
}

func TestValidateCommand(t *testing.T) {
	gatewayServer := newFakeGatewayServer()
	defer gatewayServer.Close()

	cfgPath := writeConfig(t, gatewayServer.URL, filepath.Join(t.TempDir(), "activity.jsonl"))
	defPath := writeValidDefinition(t)

	out, err := executeCLI(t, "--config", cfgPath, "validate", defPath)
	if err != nil {
		t.Fatalf("validate err = %v, output=%s", err, out)
	}
	if !strings.Contains(out, "VALID") {
		t.Fatalf("validate output = %s, want VALID", out)
	}

	badPath := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(badPath, []byte("name: bad\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(bad) error = %v", err)
	}
	out, err = executeCLI(t, "--config", cfgPath, "validate", badPath)
	if err == nil {
		t.Fatalf("validate(bad) expected error")
	}
	if !strings.Contains(out, "INVALID") {
		t.Fatalf("validate(bad) output=%s, want INVALID", out)
	}
}

func TestTestCommandDryRunCreatesReport(t *testing.T) {
	gatewayServer := newFakeGatewayServer()
	defer gatewayServer.Close()

	cfgPath := writeConfig(t, gatewayServer.URL, filepath.Join(t.TempDir(), "activity.jsonl"))
	defPath := writeValidDefinition(t)
	reportPath := filepath.Join(t.TempDir(), "report.json")

	out, err := executeCLI(t, "--config", cfgPath, "test", defPath, "--report", reportPath)
	if err != nil {
		t.Fatalf("test err = %v, output=%s", err, out)
	}
	if !strings.Contains(out, "PASS") {
		t.Fatalf("test output = %s, want PASS", out)
	}
	content, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("ReadFile(report) error = %v", err)
	}
	if !strings.Contains(string(content), "\"dry_run\": true") {
		t.Fatalf("report content = %s, want dry_run=true", string(content))
	}
}

func TestReviewCommandJSON(t *testing.T) {
	gatewayServer := newFakeGatewayServer()
	defer gatewayServer.Close()
	reviewerServer := newFakeReviewerServer()
	defer reviewerServer.Close()

	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_API_URL", reviewerServer.URL)

	cfgPath := writeConfig(t, gatewayServer.URL, filepath.Join(t.TempDir(), "activity.jsonl"))
	reportPath := filepath.Join(t.TempDir(), "report.json")
	if err := tester.SaveReport(reportPath, tester.Report{DefinitionName: "morning-brief", Passed: true, Status: tester.ReportStatusPass}); err != nil {
		t.Fatalf("SaveReport() error = %v", err)
	}

	out, err := executeCLI(t, "--config", cfgPath, "--json", "review", reportPath)
	if err != nil {
		t.Fatalf("review err = %v, output=%s", err, out)
	}
	if !strings.Contains(out, `"status": "PASS"`) {
		t.Fatalf("review output = %s, want PASS", out)
	}
}

func TestDeployCommandExecute(t *testing.T) {
	gatewayServer := newFakeGatewayServer()
	defer gatewayServer.Close()
	reviewerServer := newFakeReviewerServer()
	defer reviewerServer.Close()

	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_API_URL", reviewerServer.URL)

	activityPath := filepath.Join(t.TempDir(), "activity.jsonl")
	cfgPath := writeConfig(t, gatewayServer.URL, activityPath)
	defPath := writeValidDefinition(t)

	out, err := executeCLI(t, "--config", cfgPath, "--json", "deploy", defPath, "--execute")
	if err != nil {
		t.Fatalf("deploy err = %v, output=%s", err, out)
	}
	if !strings.Contains(out, `"action": "create"`) {
		t.Fatalf("deploy output = %s, want action=create", out)
	}

	store := ledger.New(activityPath)
	entries, err := store.Query(context.Background(), ledger.QueryFilter{Cron: "morning-brief"})
	if err != nil {
		t.Fatalf("Query(activity) error = %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("expected deployment ledger entries")
	}
}

func TestAuditAndActivityCommandsJSON(t *testing.T) {
	gatewayServer := newFakeGatewayServer()
	defer gatewayServer.Close()

	activityPath := filepath.Join(t.TempDir(), "activity.jsonl")
	cfgPath := writeConfig(t, gatewayServer.URL, activityPath)
	store := ledger.New(activityPath)
	now := time.Now().UTC()
	if err := store.Append(context.Background(), ledger.Entry{Timestamp: now, Cron: "morning-brief", Status: "success", DurationMS: 1000}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	resp, err := http.Post(gatewayServer.URL+"/crons", "application/json", strings.NewReader(`{
		"name":"morning-brief",
		"schedule":{"kind":"cron","expr":"0 9 * * *"},
		"payload":{"kind":"agentTurn","message":"Generate and send the morning brief"},
		"sessionTarget":"isolated"
	}`))
	if err != nil {
		t.Fatalf("http.Post(create cron) error = %v", err)
	}
	resp.Body.Close()

	out, err := executeCLI(t, "--config", cfgPath, "--json", "activity", "--cron", "morning-brief")
	if err != nil {
		t.Fatalf("activity err = %v, output=%s", err, out)
	}
	if !strings.Contains(out, `"command": "activity"`) {
		t.Fatalf("activity output = %s, want command field", out)
	}

	out, err = executeCLI(t, "--config", cfgPath, "--json", "audit")
	if err != nil {
		t.Fatalf("audit err = %v, output=%s", err, out)
	}
	if !strings.Contains(out, `"command": "audit"`) {
		t.Fatalf("audit output = %s, want command field", out)
	}
}

func TestHelpers(t *testing.T) {
	if got := defaultReportPath("/tmp/sample.yaml"); got != "sample.test-report.json" {
		t.Fatalf("defaultReportPath() = %s", got)
	}
	if _, err := parseRFC3339Ptr("bad-time"); err == nil {
		t.Fatalf("parseRFC3339Ptr(bad) expected error")
	}
}

type fakeReviewClient struct {
	callCount int
	models    []string
}

func (f *fakeReviewClient) Review(ctx context.Context, report tester.Report, opts reviewer.Options) (reviewer.Result, error) {
	f.callCount++
	f.models = append(f.models, opts.Model)
	if f.callCount == 1 {
		return reviewer.Result{}, errors.New("primary failed")
	}
	return reviewer.Result{Status: "PASS", Passed: true, Commentary: "fallback ok"}, nil
}

func TestReviewAdapterFallback(t *testing.T) {
	client := &fakeReviewClient{}
	adapter := &reviewAdapter{
		client:        client,
		defaultModel:  "primary-model",
		fallbackModel: "fallback-model",
	}

	result, err := adapter.Review(context.Background(), tester.Report{}, reviewer.Options{})
	if err != nil {
		t.Fatalf("Review() err = %v", err)
	}
	if !result.Passed || result.Status != "PASS" {
		t.Fatalf("result = %+v, want PASS", result)
	}
	if client.callCount != 2 {
		t.Fatalf("call count = %d, want 2", client.callCount)
	}
	if client.models[0] != "primary-model" || client.models[1] != "fallback-model" {
		t.Fatalf("models = %+v, want primary then fallback", client.models)
	}
}
