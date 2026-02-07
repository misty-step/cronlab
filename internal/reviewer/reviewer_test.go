package reviewer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/misty-step/cronlab/internal/tester"
)

func TestOpenRouterReviewSuccess(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method=%s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization header=%q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"content": `{"status":"PASS","commentary":"Output matches expected behavior","issues":[]}`,
				},
			}},
		})
	}))
	defer server.Close()

	client, err := NewOpenRouterClient("test-key", WithAPIURL(server.URL))
	if err != nil {
		t.Fatalf("NewOpenRouterClient() error = %v", err)
	}

	result, err := client.Review(context.Background(), tester.Report{DefinitionName: "cron-a", Passed: true}, Options{})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if !result.Passed || result.Status != "PASS" {
		t.Fatalf("result = %+v, want PASS", result)
	}
	if result.Model != DefaultModel {
		t.Fatalf("model = %q, want %q", result.Model, DefaultModel)
	}
}

func TestOpenRouterReviewFailJSON(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"content": `{"status":"FAIL","commentary":"Contains error output","issues":["unexpected keyword: error"]}`,
				},
			}},
		})
	}))
	defer server.Close()

	client, err := NewOpenRouterClient("test-key", WithAPIURL(server.URL))
	if err != nil {
		t.Fatalf("NewOpenRouterClient() error = %v", err)
	}

	result, err := client.Review(context.Background(), tester.Report{DefinitionName: "cron-a", Passed: false}, Options{Model: "kimi/model"})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if result.Passed || result.Status != "FAIL" {
		t.Fatalf("result = %+v, want FAIL", result)
	}
	if len(result.Issues) != 1 {
		t.Fatalf("issues = %+v, want 1 issue", result.Issues)
	}
	if result.Model != "kimi/model" {
		t.Fatalf("model = %q, want kimi/model", result.Model)
	}
}

func TestOpenRouterReviewFallbackText(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"content": "FAIL: output contains stack trace",
				},
			}},
		})
	}))
	defer server.Close()

	client, err := NewOpenRouterClient("test-key", WithAPIURL(server.URL))
	if err != nil {
		t.Fatalf("NewOpenRouterClient() error = %v", err)
	}

	result, err := client.Review(context.Background(), tester.Report{DefinitionName: "cron-a"}, Options{})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if result.Status != "FAIL" || result.Passed {
		t.Fatalf("result = %+v, want FAIL", result)
	}
}

func TestOpenRouterReviewHTTPError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad request"))
	}))
	defer server.Close()

	client, err := NewOpenRouterClient("test-key", WithAPIURL(server.URL))
	if err != nil {
		t.Fatalf("NewOpenRouterClient() error = %v", err)
	}

	_, err = client.Review(context.Background(), tester.Report{}, Options{})
	if err == nil {
		t.Fatalf("Review() expected error")
	}
}

func TestNewOpenRouterClientValidation(t *testing.T) {
	t.Parallel()

	if _, err := NewOpenRouterClient(""); err == nil {
		t.Fatalf("NewOpenRouterClient(empty) expected error")
	}
}
