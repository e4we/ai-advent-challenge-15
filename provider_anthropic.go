package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

// ─────────────────────────────────────────────
// Провайдер: Anthropic (Claude)
// ─────────────────────────────────────────────

type AnthropicProvider struct {
	apiKey string
}

func NewAnthropicProvider() *AnthropicProvider {
	key := os.Getenv(config.APIKeyEnv)
	if key == "" {
		fmt.Printf("❌ Установи API ключ: export %s=sk-ant-...\n", config.APIKeyEnv)
		os.Exit(1)
	}
	return &AnthropicProvider{apiKey: key}
}

// Структуры запроса Anthropic
type anthropicRequest struct {
	Model       string                 `json:"model"`
	MaxTokens   int                    `json:"max_tokens"`
	Temperature float64                `json:"temperature"`
	System      string                 `json:"system"`
	Messages    []map[string]interface{} `json:"messages"`
	Tools       []anthropicTool        `json:"tools"`
}

type anthropicTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

// Структуры ответа Anthropic
type anthropicResponse struct {
	Content    []anthropicContentBlock `json:"content"`
	StopReason string                 `json:"stop_reason"`
	Error      *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type anthropicContentBlock struct {
	Type  string                 `json:"type"`
	Text  string                 `json:"text,omitempty"`
	ID    string                 `json:"id,omitempty"`
	Name  string                 `json:"name,omitempty"`
	Input map[string]interface{} `json:"input,omitempty"`
}

func (p *AnthropicProvider) Chat(messages []Message) (*LLMResponse, error) {
	// Конвертируем tools (в ChatMode — пустой список)
	tools := make([]anthropicTool, 0)
	if !config.ChatMode {
		for _, t := range GetToolSchemas() {
			tools = append(tools, anthropicTool{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: t.Parameters,
			})
		}
	}

	// Конвертируем messages в формат Anthropic
	apiMessages := make([]map[string]interface{}, len(messages))
	for i, m := range messages {
		apiMessages[i] = map[string]interface{}{
			"role":    m.Role,
			"content": m.Content,
		}
	}

	reqBody := anthropicRequest{
		Model:       config.Model,
		MaxTokens:   config.MaxTokens,
		Temperature: config.Temperature,
		System:      systemPrompt,
		Messages:    apiMessages,
		Tools:       tools,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("ошибка сериализации: %w", err)
	}

	req, err := http.NewRequest("POST", config.BaseURL+"/v1/messages", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("ошибка создания запроса: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ошибка HTTP: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения ответа: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API ошибка (%d): %s", resp.StatusCode, string(body))
	}

	var apiResp anthropicResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("ошибка парсинга: %w", err)
	}

	if apiResp.Error != nil {
		return nil, fmt.Errorf("API ошибка: %s", apiResp.Error.Message)
	}

	// Парсим в единый формат
	result := &LLMResponse{StopReason: apiResp.StopReason}
	for _, block := range apiResp.Content {
		switch block.Type {
		case "text":
			result.Text += block.Text
		case "tool_use":
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: block.Input,
			})
		}
	}

	return result, nil
}

func (p *AnthropicProvider) FormatToolResult(toolCallID string, result ToolResult) Message {
	return Message{
		Role: "user",
		Content: []ContentBlock{
			{
				Type:      "tool_result",
				ToolUseID: toolCallID,
				Content:   result.Output,
				IsError:   result.IsError,
			},
		},
	}
}

func (p *AnthropicProvider) FormatAssistantMessage(resp *LLMResponse) Message {
	var content []ContentBlock

	if resp.Text != "" {
		content = append(content, ContentBlock{
			Type: "text",
			Text: resp.Text,
		})
	}

	for _, tc := range resp.ToolCalls {
		input := tc.Arguments
		if input == nil {
			input = map[string]interface{}{}
		}
		content = append(content, ContentBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Name,
			Input: input,
		})
	}

	return Message{Role: "assistant", Content: content}
}
