# CLI-агент на Go

CLI-агент, который принимает задачи от пользователя, составляет план (опционально) и выполняет их через LLM с инструментами (bash, файлы). Поддерживает Anthropic Claude и OpenAI-совместимые API (включая Ollama, LM Studio).

## Быстрый старт

```bash
# Установи API-ключ
export ANTHROPIC_API_KEY=sk-ant-...   # для Anthropic
# или
export OPENAI_API_KEY=sk-...          # для OpenAI

# Сборка
go build -o agent .

# Интерактивный REPL
./agent

# Одна команда
./agent "напиши hello world на python"
```

## Возможности

- **4 инструмента:** `run_command`, `read_file`, `write_file`, `list_files`
- **Режим планирования** (`/plan`) — LLM сначала составляет план, пользователь утверждает, потом выполнение с трекингом прогресса
- **Пошаговый режим** (`/step`) — пауза после каждого tool call
- **Чат-режим** (`/chat`) — диалог без инструментов
- **Пауза/возобновление** — `Ctrl+C` приостанавливает, `/resume` продолжает (даже после перезапуска)
- **Персистентность** — незавершённая задача сохраняется в `.agent-state.json`

## Провайдеры

Переключение в `config.go`:

| Провайдер | Model | API Key |
|-----------|-------|---------|
| Anthropic | `claude-haiku-4-5-20251001` | `ANTHROPIC_API_KEY` |
| OpenAI | `gpt-4o` | `OPENAI_API_KEY` |
| Ollama | `llama3` | — |
| LM Studio | `local-model` | — |

## Команды REPL

| Команда | Действие |
|---------|----------|
| `/help` | Справка |
| `/plan` | Вкл/выкл планирование |
| `/step` | Вкл/выкл пошаговый режим |
| `/chat` | Вкл/выкл чат-режим |
| `/resume` | Продолжить после паузы |
| `/abort` | Отменить задачу |
| `/status` | Состояние задачи |
| `/clear` | Очистить историю |
| `/exit` | Выход |

## Архитектура

```
Ввод пользователя
    → NewTask() → State: "plan" (или "execute")
    → doPlan()  → LLM генерирует JSON-план → пользователь подтверждает
    → doExecute() → Execute() loop → инструменты → LLM → ...
    → State: "done" | "pause" | "fail"
```

### Файлы

| Файл | Назначение |
|------|-----------|
| `main.go` | REPL, планирование, `PlanTracker` |
| `config.go` | Конфигурация, типы (`Provider`, `Message`, `ToolCall`, `LLMResponse`) |
| `task.go` | `Task` — конечный автомат с персистентностью |
| `executor.go` | Цикл LLM → инструменты → LLM, паттерн `ExecHooks` |
| `tools.go` | Реализация инструментов + JSON-схемы |
| `autotest.go` | Автотесты по типу файла после `write_file` |
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

## Система валидации

Три уровня валидации, ноль дополнительных API-вызовов:

### 1. Валидация плана (перед выполнением)

`validatePlan()` в `main.go` — проверяет структуру плана после парсинга: `summary`, шаги, имена инструментов, дубли номеров. Если есть ошибки — план отправляется LLM на переделку (1 попытка), затем показывается пользователю с предупреждениями.

### 2. Валидация результатов tool call (во время выполнения)

`ValidateToolResult()` в `tools.go` — после каждого tool call проверяет результат по эвристикам: `permission denied`, `command not found`, `no such file or directory`. Ставит флаг `IsError: true`, LLM получает ошибку и сам решает что делать.

### 3. Автотесты по типу файла (после write_file)

`autotest.go` — после успешной записи файла автоматически проверяет артефакт:

| Файл | Автотест |
|------|----------|
| `*_test.go` | `go test ./...` |
| `*.go` | `go build .` |
| `*.py` | `python3 -c "import ast; ast.parse(...)"` |
| `Dockerfile` | `docker build --check .` |
| `*.json` | встроенный `json.Valid()` |

Результат автотеста передаётся LLM как user-сообщение `[AUTOTEST OK: ...]` или `[AUTOTEST FAILED: ...]`. LLM сам решает: исправить код, попробовать другой подход или продолжить.

### ExecHooks

`Execute()` принимает `ExecHooks` — набор callback'ов:

- `OnLLMResponse` — вывод текста LLM
- `OnToolResult` — вывод результата инструмента (наблюдатель)
- `AfterToolResult` — автотест, возвращает `*ToolResult` (добавляется в `task.Messages` если не nil)
- `OnNoToolCalls` — напоминание LLM о невыполненных шагах
- `ShouldStop` — досрочный выход при завершении плана
- `OnPause`, `OnComplete`, `OnError` — события жизненного цикла

Два готовых набора: `DefaultHooks()` (без плана) и `TrackedHooks()` (с трекингом прогресса).
