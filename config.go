package main

import "encoding/json"

// ─────────────────────────────────────────────
// КОНФИГУРАЦИЯ — выбери своего провайдера
// ─────────────────────────────────────────────

// Провайдер LLM: "anthropic", "openai", "local"
// Для переключения — измени значения ниже.
var config = Config{
	// --- Anthropic (Claude) ---
	Provider:  "anthropic",
	Model:     "claude-haiku-4-5-20251001",
	APIKeyEnv: "ANTHROPIC_API_KEY",
	BaseURL:   "https://api.anthropic.com",

	// --- OpenAI (GPT) ---
	// Provider:  "openai",
	// Model:     "gpt-4o",
	// APIKeyEnv: "OPENAI_API_KEY",
	// BaseURL:   "https://api.openai.com",

	// --- Ollama (локальная) ---
	// Provider:  "local",
	// Model:     "llama3",
	// APIKeyEnv: "",
	// BaseURL:   "http://localhost:11434",

	// --- LM Studio (локальная) ---
	// Provider:  "local",
	// Model:     "local-model",
	// APIKeyEnv: "",
	// BaseURL:   "http://localhost:1234",

	MaxTokens:   3000,
	Temperature: 0.0,
	PlanMode:    false, // true = сначала план, потом выполнение
	StepMode:    false, // true = пауза после каждого tool call
}

type Config struct {
	Provider    string
	Model       string
	APIKeyEnv   string
	BaseURL     string
	MaxTokens   int
	Temperature float64
	PlanMode    bool // режим планирования
	StepMode    bool // пошаговый режим (пауза после каждого tool call)
	ChatMode    bool // режим чата без инструментов
}

// ─────────────────────────────────────────────
// Единый формат ответа от любого провайдера
// ─────────────────────────────────────────────

type LLMResponse struct {
	Text       string
	ToolCalls  []ToolCall
	StopReason string
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments map[string]interface{}
}

// Интерфейс провайдера — все провайдеры реализуют его
type Provider interface {
	Chat(messages []Message) (*LLMResponse, error)
	FormatToolResult(toolCallID string, result ToolResult) Message
	FormatAssistantMessage(resp *LLMResponse) Message
}

// Универсальное сообщение
type Message struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string или []ContentBlock
}

// UnmarshalJSON — кастомная десериализация Message.
// Нужна потому что Content может быть строкой или массивом ContentBlock.
// Без этого json.Unmarshal превратит массив в []interface{},
// а нам нужен []ContentBlock для отправки в API.
func (m *Message) UnmarshalJSON(data []byte) error {
	// Парсим в промежуточную структуру с RawMessage
	var raw struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.Role = raw.Role

	// Пробуем как строку
	var s string
	if err := json.Unmarshal(raw.Content, &s); err == nil {
		m.Content = s
		return nil
	}

	// Пробуем как массив ContentBlock
	var blocks []ContentBlock
	if err := json.Unmarshal(raw.Content, &blocks); err == nil {
		m.Content = blocks
		return nil
	}

	// Фоллбэк: оставляем как есть
	var any interface{}
	json.Unmarshal(raw.Content, &any)
	m.Content = any
	return nil
}

type ContentBlock struct {
	Type      string                 `json:"type"`
	Text      string                 `json:"text,omitempty"`
	ID        string                 `json:"id,omitempty"`
	Name      string                 `json:"name,omitempty"`
	Input     map[string]interface{} `json:"input,omitempty"`
	ToolUseID string                 `json:"tool_use_id,omitempty"`
	Content   string                 `json:"content,omitempty"`
	IsError   bool                   `json:"is_error,omitempty"`
}
