// Package provider implements the small OpenAI-compatible protocol surface
// YCode needs. Keeping it in the standard library makes the final binary easy to
// audit and quick to start.
package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolCall struct {
	Index    int          `json:"index,omitempty"`
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

type FunctionSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ToolSpec struct {
	Type     string       `json:"type"`
	Function FunctionSpec `json:"function"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type Turn struct {
	Content   string
	ToolCalls []ToolCall
	Usage     Usage
}

type Request struct {
	Model       string
	Messages    []Message
	Tools       []ToolSpec
	MaxTokens   int
	Stream      bool
	OnTextDelta func(string)
}

type Completer interface {
	Complete(context.Context, Request) (Turn, error)
}

type Client struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
	UserAgent  string
}

func NewClient(baseURL, apiKey string, timeout time.Duration) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		HTTPClient: &http.Client{
			Timeout: timeout,
		},
		UserAgent: "ycode/0.1",
	}
}

type wireRequest struct {
	Model     string     `json:"model"`
	Messages  []Message  `json:"messages"`
	Tools     []ToolSpec `json:"tools,omitempty"`
	MaxTokens int        `json:"max_tokens,omitempty"`
	Stream    bool       `json:"stream"`
}

func (c *Client) Complete(ctx context.Context, request Request) (Turn, error) {
	if c == nil {
		return Turn{}, errors.New("provider client is nil")
	}
	body, err := json.Marshal(wireRequest{
		Model:     request.Model,
		Messages:  request.Messages,
		Tools:     request.Tools,
		MaxTokens: request.MaxTokens,
		Stream:    request.Stream,
	})
	if err != nil {
		return Turn{}, fmt.Errorf("encode provider request: %w", err)
	}

	endpoint := c.BaseURL
	if !strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint += "/chat/completions"
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Turn{}, fmt.Errorf("create provider request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json, text/event-stream")
	if c.APIKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if c.UserAgent != "" {
		httpRequest.Header.Set("User-Agent", c.UserAgent)
	}

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return Turn{}, fmt.Errorf("provider request failed: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 32<<10))
		message := strings.TrimSpace(string(data))
		if message == "" {
			message = http.StatusText(response.StatusCode)
		}
		return Turn{}, fmt.Errorf("provider returned %s: %s", response.Status, message)
	}

	contentType := response.Header.Get("Content-Type")
	if request.Stream && strings.Contains(contentType, "text/event-stream") {
		return decodeStream(response.Body, request.OnTextDelta)
	}
	if request.Stream && contentType == "" {
		return decodeUnknown(response.Body, request.OnTextDelta)
	}
	return decodeJSON(io.LimitReader(response.Body, 16<<20), request.OnTextDelta)
}

type wireResponse struct {
	Choices []struct {
		Message struct {
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func decodeJSON(reader io.Reader, onDelta func(string)) (Turn, error) {
	var response wireResponse
	if err := json.NewDecoder(reader).Decode(&response); err != nil {
		return Turn{}, fmt.Errorf("decode provider response: %w", err)
	}
	if response.Error != nil {
		return Turn{}, errors.New(response.Error.Message)
	}
	if len(response.Choices) == 0 {
		return Turn{}, errors.New("provider response contained no choices")
	}
	message := response.Choices[0].Message
	if onDelta != nil && message.Content != "" {
		onDelta(message.Content)
	}
	return Turn{Content: message.Content, ToolCalls: message.ToolCalls, Usage: response.Usage}, nil
}

func decodeUnknown(reader io.Reader, onDelta func(string)) (Turn, error) {
	buffered := bufio.NewReader(reader)
	for {
		next, err := buffered.Peek(1)
		if err != nil {
			return Turn{}, fmt.Errorf("read provider response: %w", err)
		}
		if next[0] == ' ' || next[0] == '\t' || next[0] == '\r' || next[0] == '\n' {
			_, _ = buffered.ReadByte()
			continue
		}
		if next[0] == '{' {
			return decodeJSON(io.LimitReader(buffered, 16<<20), onDelta)
		}
		return decodeStream(buffered, onDelta)
	}
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func decodeStream(reader io.Reader, onDelta func(string)) (Turn, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), 2<<20)

	var turn Turn
	var calls []ToolCall
	totalBytes := 0
	for scanner.Scan() {
		totalBytes += len(scanner.Bytes())
		if totalBytes > 32<<20 {
			return Turn{}, errors.New("provider stream exceeded 32 MiB")
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return Turn{}, fmt.Errorf("decode provider stream: %w", err)
		}
		if chunk.Error != nil {
			return Turn{}, errors.New(chunk.Error.Message)
		}
		if chunk.Usage.TotalTokens != 0 || chunk.Usage.PromptTokens != 0 || chunk.Usage.CompletionTokens != 0 {
			turn.Usage = chunk.Usage
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta
		if delta.Content != "" {
			turn.Content += delta.Content
			if onDelta != nil {
				onDelta(delta.Content)
			}
		}
		for _, incoming := range delta.ToolCalls {
			index := incoming.Index
			for len(calls) <= index {
				calls = append(calls, ToolCall{Index: len(calls), Type: "function"})
			}
			current := &calls[index]
			if incoming.ID != "" {
				current.ID += incoming.ID
			}
			if incoming.Type != "" {
				current.Type = incoming.Type
			}
			if incoming.Function.Name != "" {
				current.Function.Name += incoming.Function.Name
			}
			if incoming.Function.Arguments != "" {
				current.Function.Arguments += incoming.Function.Arguments
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Turn{}, fmt.Errorf("read provider stream: %w", err)
	}
	turn.ToolCalls = calls
	if turn.Content == "" && len(turn.ToolCalls) == 0 {
		return Turn{}, errors.New("provider stream ended without content or tool calls")
	}
	return turn, nil
}
