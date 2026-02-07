package reviewer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/misty-step/cronlab/internal/tester"
)

const (
	DefaultModel      = "deepseek/deepseek-chat-v3-0324"
	defaultOpenRouter = "https://openrouter.ai/api/v1/chat/completions"
)

type Client interface {
	Review(ctx context.Context, report tester.Report, opts Options) (Result, error)
}

type Options struct {
	Model string
}

type Result struct {
	ReviewedAt  time.Time `json:"reviewed_at"`
	Model       string    `json:"model"`
	Status      string    `json:"status"`
	Passed      bool      `json:"passed"`
	Commentary  string    `json:"commentary"`
	Issues      []string  `json:"issues,omitempty"`
	RawResponse string    `json:"raw_response,omitempty"`
}

type OpenRouterClient struct {
	apiKey     string
	apiURL     string
	httpClient *http.Client
	logger     *slog.Logger
}

type OpenRouterOption func(*OpenRouterClient)

func WithAPIURL(apiURL string) OpenRouterOption {
	return func(c *OpenRouterClient) {
		if strings.TrimSpace(apiURL) != "" {
			c.apiURL = apiURL
		}
	}
}

func WithHTTPClient(client *http.Client) OpenRouterOption {
	return func(c *OpenRouterClient) {
		if client != nil {
			c.httpClient = client
		}
	}
}

func WithLogger(logger *slog.Logger) OpenRouterOption {
	return func(c *OpenRouterClient) {
		if logger != nil {
			c.logger = logger
		}
	}
}

func NewOpenRouterClient(apiKey string, opts ...OpenRouterOption) (*OpenRouterClient, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, errors.New("openrouter api key is required")
	}

	client := &OpenRouterClient{
		apiKey:     apiKey,
		apiURL:     defaultOpenRouter,
		httpClient: &http.Client{Timeout: 45 * time.Second},
		logger:     slog.Default(),
	}
	for _, opt := range opts {
		opt(client)
	}
	return client, nil
}

func NewOpenRouterClientFromEnv(opts ...OpenRouterOption) (*OpenRouterClient, error) {
	return NewOpenRouterClient(os.Getenv("OPENROUTER_API_KEY"), opts...)
}

func (c *OpenRouterClient) Review(ctx context.Context, report tester.Report, opts Options) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	model := strings.TrimSpace(opts.Model)
	if model == "" {
		model = DefaultModel
	}

	reportJSON, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return Result{}, fmt.Errorf("marshal test report: %w", err)
	}

	requestBody := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "You are CronLab QA reviewer. Evaluate cron test output and return strict JSON with fields: status(PASS|FAIL), commentary(string), issues(array of strings).",
			},
			{
				"role":    "user",
				"content": "Review this cron test report for correctness, anomalies, and failure patterns:\n\n" + string(reportJSON),
			},
		},
		"temperature": 0.1,
	}

	payload, err := json.Marshal(requestBody)
	if err != nil {
		return Result{}, fmt.Errorf("marshal reviewer request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL, bytes.NewReader(payload))
	if err != nil {
		return Result{}, fmt.Errorf("create reviewer request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("HTTP-Referer", "https://cronlab.local")
	req.Header.Set("X-Title", "CronLab")

	c.logger.Debug("sending review request", "model", model)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("send reviewer request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return Result{}, fmt.Errorf("read reviewer response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{}, fmt.Errorf("reviewer request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	content, err := parseCompletionContent(respBody)
	if err != nil {
		return Result{}, err
	}
	parsed := parseReviewContent(content)
	parsed.Model = model
	parsed.ReviewedAt = time.Now().UTC()
	return parsed, nil
}

func parseCompletionContent(body []byte) (string, error) {
	var payload struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("decode reviewer response: %w", err)
	}
	if len(payload.Choices) == 0 {
		return "", errors.New("reviewer response missing choices")
	}
	content := strings.TrimSpace(payload.Choices[0].Message.Content)
	if content == "" {
		return "", errors.New("reviewer response empty content")
	}
	return content, nil
}

func parseReviewContent(content string) Result {
	result := Result{RawResponse: content}
	content = strings.TrimSpace(content)
	if content == "" {
		result.Status = "FAIL"
		result.Passed = false
		result.Commentary = "Reviewer returned empty response"
		result.Issues = []string{"empty reviewer response"}
		return result
	}

	cleaned := content
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	var parsed struct {
		Status     string   `json:"status"`
		Commentary string   `json:"commentary"`
		Issues     []string `json:"issues"`
	}
	if err := json.Unmarshal([]byte(cleaned), &parsed); err == nil {
		status := normalizeStatus(parsed.Status)
		if status == "" {
			status = inferStatusFromText(cleaned)
		}
		if status == "" {
			status = "FAIL"
		}
		result.Status = status
		result.Passed = status == "PASS"
		result.Commentary = strings.TrimSpace(parsed.Commentary)
		if result.Commentary == "" {
			if result.Passed {
				result.Commentary = "Reviewer approved the test output"
			} else {
				result.Commentary = "Reviewer rejected the test output"
			}
		}
		result.Issues = parsed.Issues
		if result.Passed {
			result.Issues = nil
		}
		return result
	}

	status := inferStatusFromText(cleaned)
	if status == "" {
		status = "FAIL"
	}
	result.Status = status
	result.Passed = status == "PASS"
	result.Commentary = cleaned
	if !result.Passed {
		result.Issues = []string{"non-JSON reviewer output"}
	}
	return result
}

func normalizeStatus(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "PASS":
		return "PASS"
	case "FAIL":
		return "FAIL"
	default:
		return ""
	}
}

func inferStatusFromText(text string) string {
	upper := strings.ToUpper(text)
	switch {
	case strings.Contains(upper, "\"STATUS\":\"PASS\"") || strings.Contains(upper, " PASS") || strings.HasPrefix(upper, "PASS"):
		return "PASS"
	case strings.Contains(upper, "\"STATUS\":\"FAIL\"") || strings.Contains(upper, " FAIL") || strings.HasPrefix(upper, "FAIL"):
		return "FAIL"
	default:
		return ""
	}
}
