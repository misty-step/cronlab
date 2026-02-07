package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/misty-step/cronlab/internal/definition"
)

type HTTPClient struct {
	baseURL    *url.URL
	httpClient *http.Client
	logger     *slog.Logger
}

type HTTPClientOption func(*HTTPClient)

func WithHTTPClient(client *http.Client) HTTPClientOption {
	return func(c *HTTPClient) {
		if client != nil {
			c.httpClient = client
		}
	}
}

func WithLogger(logger *slog.Logger) HTTPClientOption {
	return func(c *HTTPClient) {
		if logger != nil {
			c.logger = logger
		}
	}
}

func NewHTTPClient(baseURL string, opts ...HTTPClientOption) (*HTTPClient, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return nil, fmt.Errorf("base URL is required")
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid gateway URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid gateway URL: must include scheme and host")
	}

	client := &HTTPClient{
		baseURL:    parsed,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		logger:     slog.Default(),
	}
	for _, opt := range opts {
		opt(client)
	}
	return client, nil
}

func (c *HTTPClient) List(ctx context.Context) ([]Cron, error) {
	var out []Cron
	if err := c.doJSON(ctx, http.MethodGet, "/crons", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *HTTPClient) Get(ctx context.Context, id string) (Cron, error) {
	var out Cron
	if err := c.doJSON(ctx, http.MethodGet, "/crons/"+url.PathEscape(id), nil, &out); err != nil {
		return Cron{}, err
	}
	return out, nil
}

func (c *HTTPClient) Create(ctx context.Context, def definition.Definition) (Cron, error) {
	var out Cron
	if err := c.doJSON(ctx, http.MethodPost, "/crons", def, &out); err != nil {
		return Cron{}, err
	}
	return out, nil
}

func (c *HTTPClient) Update(ctx context.Context, id string, def definition.Definition) (Cron, error) {
	var out Cron
	if err := c.doJSON(ctx, http.MethodPut, "/crons/"+url.PathEscape(id), def, &out); err != nil {
		return Cron{}, err
	}
	return out, nil
}

func (c *HTTPClient) Delete(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodDelete, "/crons/"+url.PathEscape(id), nil, nil)
}

func (c *HTTPClient) Run(ctx context.Context, id string) (RunResult, error) {
	var out RunResult
	if err := c.doJSON(ctx, http.MethodPost, "/crons/"+url.PathEscape(id)+"/run", map[string]any{}, &out); err != nil {
		return RunResult{}, err
	}
	return out, nil
}

func (c *HTTPClient) doJSON(ctx context.Context, method string, path string, in any, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	endpoint := c.baseURL.ResolveReference(&url.URL{Path: path})

	var body io.Reader
	if in != nil {
		payload, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		body = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	c.logger.Debug("gateway request", "method", method, "url", endpoint.String())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := parseErrorMessage(respBody)
		if message == "" {
			message = string(respBody)
		}
		return HTTPError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(message)}
	}
	if out == nil || len(respBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func parseErrorMessage(body []byte) string {
	var payload struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	if strings.TrimSpace(payload.Error) != "" {
		return payload.Error
	}
	if strings.TrimSpace(payload.Message) != "" {
		return payload.Message
	}
	return ""
}
