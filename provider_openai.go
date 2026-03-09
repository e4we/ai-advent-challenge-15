package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// ─────────────────────────────────────────────
// Провайдер: OpenAI / Ollama / LM Studio
// ─────────────────────────────────────────────

type OpenAIProvider struct {
	apiKey  string
	baseURL string
}

func NewOpenAIProvider() *OpenAIProvider {
	apiKey := "not-needed"
	if config.APIKeyEnv != "" {
		key := os.Getenv(config.APIKeyEnv)
		if key != "" {
			apiKey = key
		} else if config.Provider == "openai" {
			fmt.Printf("❌ Установи API ключ: export %s=sk-...\n", config.APIKeyEnv)
			os.Exit(1)
		}
	}

	baseURL := config.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}

	return &OpenAIProvider{apiKey: apiKey, baseURL: baseURL}
}

// ── Структуры запроса OpenAI ──

type openaiRequest struct {
	Model       string              `json:"model"`
	Messages    []openaiMessage     `json:"messages"`
	MaxTokens   int                 `json:"max_tokens"`
	Temperature float64             `json:"temperature"`
	Tools       []openaiToolDef     `json:"tools,omitempty"`
}

type openaiMessage struct {
	Role       string              `json:"role"`
	Content    *string             `json:"content"`
	ToolCalls  []openaiToolCall    `json:"tool_calls,omitempty"`
	ToolCallID string              `json:"tool_call_id,omitempty"`
}

type openaiToolDef struct {
	Type     string           `json:"type"`
	Function openaiFunction   `json:"function"`
}

type openaiFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type openaiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// ── Структуры ответа OpenAI ──

type openaiResponse struct {
	Choices []struct {
		Message      openaiMessage `json:"message"`
		FinishReason string        `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (p *OpenAIProvider) Chat(messages []Message) (*LLMResponse, error) {
	// Конвертируем tools (в ChatMode — nil, omitempty уберёт из JSON)
	var tools []openaiToolDef
	if !config.ChatMode {
		for _, t := range GetToolSchemas() {
			tools = append(tools, openaiToolDef{
				Type: "function",
				Function: openaiFunction{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.Parameters,
				},
			})
		}
	}

	// Конвертируем messages
	sysContent := systemPrompt
	apiMessages := []openaiMessage{
		{Role: "system", Content: &sysContent},
	}

	for _, m := range messages {
		msg := openaiMessage{Role: m.Role}

		switch content := m.Content.(type) {
		case string:
			msg.Content = &content
		case []ContentBlock:
			// Формат OpenAI tool results
			for _, block := range content {
				if block.Type == "tool_result" {
					msg = openaiMessage{
						Role:       "tool",
						Content:    &block.Content,
						ToolCallID: block.ToolUseID,
					}
				}
			}
		default:
			// Для assistant messages с tool_calls из истории
			jsonBytes, _ := json.Marshal(content)
			var blocks []ContentBlock
			if json.Unmarshal(jsonBytes, &blocks) == nil {
				var text string
				var toolCalls []openaiToolCall
				for _, b := range blocks {
					if b.Type == "text" {
						text = b.Text
					} else if b.Type == "tool_use" {
						argsJSON, _ := json.Marshal(b.Input)
						toolCalls = append(toolCalls, openaiToolCall{
							ID:   b.ID,
							Type: "function",
							Function: struct {
								Name      string `json:"name"`
								Arguments string `json:"arguments"`
							}{
								Name:      b.Name,
								Arguments: string(argsJSON),
							},
						})
					}
				}
				if text != "" {
					msg.Content = &text
				}
				msg.ToolCalls = toolCalls
			}
		}

		apiMessages = append(apiMessages, msg)
	}

	// Определяем URL
	apiURL := p.baseURL + "/v1/chat/completions"

	reqBody := openaiRequest{
		Model:       config.Model,
		Messages:    apiMessages,
		MaxTokens:   config.MaxTokens,
		Temperature: config.Temperature,
		Tools:       tools,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("ошибка сериализации: %w", err)
	}

	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("ошибка создания запроса: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "not-needed" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ошибка HTTP: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API ошибка (%d): %s", resp.StatusCode, string(body))
	}

	var apiResp openaiResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("ошибка парсинга: %w", err)
	}

	if apiResp.Error != nil {
		return nil, fmt.Errorf("API ошибка: %s", apiResp.Error.Message)
	}

	if len(apiResp.Choices) == 0 {
		return nil, fmt.Errorf("пустой ответ от API")
	}

	msg := apiResp.Choices[0].Message
	result := &LLMResponse{
		StopReason: apiResp.Choices[0].FinishReason,
	}

	if msg.Content != nil {
		result.Text = *msg.Content
	}

	for _, tc := range msg.ToolCalls {
		var args map[string]interface{}
		json.Unmarshal([]byte(tc.Function.Arguments), &args)
		result.ToolCalls = append(result.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: args,
		})
	}

	// Фоллбэк для локальных моделей: парсим JSON из текста
	if len(result.ToolCalls) == 0 && result.Text != "" {
		result.ToolCalls = tryParseToolFromText(result.Text)
		if len(result.ToolCalls) > 0 {
			idx := strings.Index(result.Text, "{")
			if idx > 0 {
				result.Text = strings.TrimSpace(result.Text[:idx])
			} else {
				result.Text = ""
			}
		}
	}

	return result, nil
}

func tryParseToolFromText(text string) []ToolCall {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(text[start:end+1]), &parsed); err != nil {
		return nil
	}

	toolName, ok := parsed["tool"].(string)
	if !ok {
		return nil
	}

	args, _ := parsed["arguments"].(map[string]interface{})
	if args == nil {
		args = map[string]interface{}{}
	}
	return []ToolCall{{
		ID:        fmt.Sprintf("local_%d", len(text)),
		Name:      toolName,
		Arguments: args,
	}}
}

func (p *OpenAIProvider) FormatToolResult(toolCallID string, result ToolResult) Message {
	output := result.Output
	if result.IsError {
		output = "[ERROR] " + output
	}
	return Message{
		Role: "user",
		Content: []ContentBlock{
			{
				Type:      "tool_result",
				ToolUseID: toolCallID,
				Content:   output,
			},
		},
	}
}

func (p *OpenAIProvider) FormatAssistantMessage(resp *LLMResponse) Message {
	var content []ContentBlock

	if resp.Text != "" {
		content = append(content, ContentBlock{
			Type: "text",
			Text: resp.Text,
		})
	}

	for _, tc := range resp.ToolCalls {
		content = append(content, ContentBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Name,
			Input: tc.Arguments,
		})
	}

	return Message{Role: "assistant", Content: content}
}
