// Package llm is a minimal OpenAI-compatible chat-completions client. AutoSkills is never in
// the inference path (PRD §10.4): the endpoint is whatever the user configures — Anthropic,
// OpenAI, a corporate gateway, or local Ollama — all speaking the same wire shape.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	Endpoint string // base URL, e.g. https://api.anthropic.com/v1
	APIKey   string
	Model    string
	HTTP     *http.Client
}

func New(endpoint, apiKey, model string) *Client {
	return &Client{
		Endpoint: strings.TrimRight(endpoint, "/"),
		APIKey:   apiKey,
		Model:    model,
		HTTP:     &http.Client{Timeout: 180 * time.Second},
	}
}

type chatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Chat sends a system+user exchange and returns the assistant text. Retries transient failures.
func (c *Client) Chat(ctx context.Context, system, user string) (string, error) {
	req := chatRequest{
		Model:       c.Model,
		Temperature: 0.2,
		MaxTokens:   8192,
		Messages: []Message{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	}
	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(attempt*attempt) * 2 * time.Second):
			}
		}
		out, retryable, err := c.once(ctx, body)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if !retryable {
			break
		}
	}
	return "", lastErr
}

func (c *Client) once(ctx context.Context, body []byte) (out string, retryable bool, err error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", false, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
		// Anthropic's OpenAI-compat layer accepts Authorization; the native header is harmless elsewhere.
		httpReq.Header.Set("x-api-key", c.APIKey)
	}
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return "", true, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", true, err
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return "", true, fmt.Errorf("llm: %s: %s", resp.Status, truncate(string(raw), 300))
	}
	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("llm: %s: %s", resp.Status, truncate(string(raw), 300))
	}
	var cr chatResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return "", false, fmt.Errorf("llm: decode response: %w", err)
	}
	if cr.Error != nil {
		return "", false, fmt.Errorf("llm: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return "", false, fmt.Errorf("llm: empty choices")
	}
	return cr.Choices[0].Message.Content, false, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
