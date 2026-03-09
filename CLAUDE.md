# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

CLI-агент на Go, который принимает задачи от пользователя, составляет план (опционально) и выполняет их через LLM с инструментами (bash, файлы). Поддерживает Anthropic Claude и OpenAI-совместимые API (включая Ollama, LM Studio).

## Commands

```bash
# Сборка
go build -o agent .

# Запуск (интерактивный REPL)
go run .

# Запуск одной команды
go run . "напиши hello world на python"

# Зависимости
go mod tidy
```

API ключ задаётся через переменную окружения:
```bash
export ANTHROPIC_API_KEY=sk-ant-...   # для Anthropic
export OPENAI_API_KEY=sk-...          # для OpenAI
```

## Architecture

### Поток выполнения задачи

```
Ввод пользователя
    → NewTask() → State: "plan" (или "execute" если PlanMode=false)
    → doPlan()  → LLM генерирует JSON-план → пользователь подтверждает
    → doExecute() → Execute() loop → инструменты → LLM → ...
    → State: "done" | "pause" | "fail"
```

### Файлы

| Файл | Назначение |
|------|-----------|
| `config.go` | Конфигурация провайдера + общие типы (`Provider` interface, `Message`, `ContentBlock`, `LLMResponse`, `ToolCall`) |
| `task.go` | `Task` как конечный автомат с персистентностью в `.agent-state.json` |
| `executor.go` | Основной цикл LLM→инструменты→LLM с паттерном `ExecHooks` |
| `main.go` | REPL, планирование, `PlanTracker` для отслеживания прогресса |
| `tools.go` | Реализация инструментов + JSON-схемы для LLM |
| `provider_anthropic.go` | Anthropic Messages API |
| `provider_openai.go` | OpenAI Chat Completions API + fallback для локальных моделей |

### Task State Machine

```
plan → review → execute → done
              ↑      ↓
              plan  pause → execute
                         → done
plan → fail
execute → fail
```

Переходы строго контролируются картой `transitions` в `task.go`. Незавершённая задача сохраняется в `.agent-state.json` и восстанавливается при следующем запуске (команда `resume`).

### Provider Interface

Все провайдеры реализуют `Provider` (в `config.go`):
- `Chat(messages []Message) (*LLMResponse, error)` — отправка запроса
- `FormatToolResult(toolCallID, result string) Message` — формат результата инструмента
- `FormatAssistantMessage(resp *LLMResponse) Message` — формат ответа ассистента

`Message.Content` может быть `string` или `[]ContentBlock` — кастомный `UnmarshalJSON` обрабатывает оба случая.

### ExecHooks Pattern

`Execute()` в `executor.go` принимает `ExecHooks` — набор callback'ов для наблюдателей:
- `DefaultHooks()` — базовый вывод без трекинга плана
- `TrackedHooks(tracker, maxReminders)` — с отслеживанием шагов плана и напоминаниями LLM

Лимит итераций: **10** (`maxIter` в `executor.go`). Таймаут команды: **30 сек**.

## Configuration

Провайдер настраивается хардкодом в `config.go` (переключение между блоками с комментариями):
- `Provider`: `"anthropic"` | `"openai"` | `"local"`
- `PlanMode`: `true` — сначала план, потом выполнение
- `StepMode`: `false` — пауза после каждого tool call

Для локальных моделей (Ollama/LM Studio) используется провайдер `"local"` с `APIKeyEnv: ""` и соответствующим `BaseURL`. `OpenAIProvider` включает fallback: парсинг tool call из JSON в тексте ответа для моделей без нативной поддержки function calling.
