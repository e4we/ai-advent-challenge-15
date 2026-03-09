package main

import (
	"fmt"
)

// ─────────────────────────────────────────────
// EXECUTOR — цикл выполнения с хуками
//
// Executor работает с Task напрямую:
//   - Обновляет task.Messages после каждого шага
//   - Обновляет task.Iteration
//   - Вызывает task.Pause() / task.Complete() / task.Fail()
//   - Сохраняет task на диск после каждого изменения
//
// Наблюдатели подключаются через ExecHooks.
// ─────────────────────────────────────────────

// ExecHooks — callback'и для наблюдателей.
type ExecHooks struct {
	OnLLMResponse   func(text string, toolCalls []ToolCall)
	OnToolResult    func(call ToolCall, result ToolResult)
	AfterToolResult func(call ToolCall, result ToolResult) *ToolResult // не-nil → добавить как user-сообщение
	OnNoToolCalls   func(text string) string                          // непустая строка = напоминание
	OnPause         func(iteration int)
	OnComplete      func()
	OnError         func(err error)
	ShouldStop      func() bool
}

// Execute — основной цикл. Работает с Task.
func Execute(task *Task, provider Provider, hooks ExecHooks) {
	maxIter := 10

	for i := task.Iteration; i < maxIter; i++ {
		task.Iteration = i

		// ── Пауза (Ctrl+C) ──
		if isPaused() {
			clearPause()
			if hooks.OnPause != nil {
				hooks.OnPause(i)
			}
			task.Pause()
			task.Save()
			return
		}

		// ── Запрос к LLM ──
		response, err := provider.Chat(task.Messages)
		if err != nil {
			if hooks.OnError != nil {
				hooks.OnError(err)
			}
			task.Fail()
			task.Save()
			return
		}

		// ── Хук: ответ LLM ──
		if hooks.OnLLMResponse != nil {
			hooks.OnLLMResponse(response.Text, response.ToolCalls)
		}

		// ── Нет tool calls → LLM остановился ──
		if len(response.ToolCalls) == 0 {
			task.Messages = append(task.Messages, provider.FormatAssistantMessage(response))

			if hooks.OnNoToolCalls != nil {
				reminder := hooks.OnNoToolCalls(response.Text)
				if reminder != "" {
					task.Messages = append(task.Messages, Message{Role: "user", Content: reminder})
					task.Save()
					continue
				}
			}

			task.Complete()
			task.Save()
			return
		}

		task.Messages = append(task.Messages, provider.FormatAssistantMessage(response))

		// ── Выполняем инструменты ──
		for _, tc := range response.ToolCalls {
			result := ExecuteTool(tc.Name, tc.Arguments)
			result = ValidateToolResult(tc.Name, result)

			if hooks.OnToolResult != nil {
				hooks.OnToolResult(tc, result)
			}

			task.Messages = append(task.Messages, provider.FormatToolResult(tc.ID, result))

			// Автотест
			if hooks.AfterToolResult != nil {
				if autoResult := hooks.AfterToolResult(tc, result); autoResult != nil {
					var msg string
					if autoResult.IsError {
						msg = fmt.Sprintf("[AUTOTEST FAILED: %s]\n%s", autoResult.Output, autoResult.Output)
					} else {
						msg = fmt.Sprintf("[AUTOTEST OK: %s]", autoResult.Output)
					}
					task.Messages = append(task.Messages, Message{Role: "user", Content: msg})
				}
			}
		}

		// Сохраняем после каждой итерации (защита от крэша)
		task.Save()

		// ── Пошаговый режим ──
		action := stepCheck(i)
		switch action {
		case "pause":
			if hooks.OnPause != nil {
				hooks.OnPause(i + 1)
			}
			task.Iteration = i + 1
			task.Pause()
			task.Save()
			return
		case "abort":
			task.Complete()
			task.Save()
			if hooks.OnComplete != nil {
				hooks.OnComplete()
			}
			return
		}

		// ── Досрочный выход ──
		if hooks.ShouldStop != nil && hooks.ShouldStop() {
			task.Complete()
			task.Save()
			break
		}
	}

	// Если дошли до лимита итераций
	if task.State == StateExecute {
		task.Complete()
		task.Save()
	}

	if hooks.OnComplete != nil {
		hooks.OnComplete()
	}
}

// ─────────────────────────────────────────────
// ГОТОВЫЕ НАБОРЫ ХУКОВ
// ─────────────────────────────────────────────

// DefaultHooks — для обычного режима (без плана).
func DefaultHooks() ExecHooks {
	return ExecHooks{
		OnLLMResponse: func(text string, toolCalls []ToolCall) {
			if text != "" {
				fmt.Printf("\n🤖 %s\n", text)
			}
		},
		OnToolResult: func(call ToolCall, result ToolResult) {
			fmt.Printf("\n  🔨 Инструмент: %s\n", call.Name)
			display := result.Output
			if len(display) > 200 {
				display = display[:200] + "..."
			}
			if result.IsError {
				fmt.Printf("  ⚠️  Результат (ошибка): %s\n", display)
			} else {
				fmt.Printf("  📋 Результат: %s\n", display)
			}
		},
		AfterToolResult: autotestHook(),
		OnError: func(err error) {
			fmt.Printf("\n❌ Ошибка API: %v\n", err)
		},
		OnPause: func(iteration int) {
			fmt.Printf("\n⏸  Агент приостановлен на итерации %d\n", iteration+1)
			fmt.Println("   Введи /resume чтобы продолжить, или /abort чтобы прервать")
		},
	}
}

// TrackedHooks — для режима с планом.
func TrackedHooks(tracker *PlanTracker, maxReminders int) ExecHooks {
	reminders := 0

	return ExecHooks{
		OnLLMResponse: func(text string, toolCalls []ToolCall) {
			if text != "" {
				fmt.Printf("\n🤖 %s\n", text)
				if tracker.UpdateFromText(text) {
					tracker.PrintProgress()
				}
			}
		},
		OnToolResult: func(call ToolCall, result ToolResult) {
			fmt.Printf("\n  🔨 Инструмент: %s\n", call.Name)
			display := result.Output
			if len(display) > 200 {
				display = display[:200] + "..."
			}
			if result.IsError {
				fmt.Printf("  ⚠️  Результат (ошибка): %s\n", display)
			} else {
				fmt.Printf("  📋 Результат: %s\n", display)
			}
		},
		AfterToolResult: autotestHook(),
		OnNoToolCalls: func(text string) string {
			if !tracker.AllDone() && reminders < maxReminders {
				pending := tracker.PendingSteps()
				reminders++
				fmt.Printf("\n  ⚠️  LLM остановился, но %d шагов не выполнено. Напоминаю...\n", len(pending))
				tracker.PrintProgress()
				return tracker.ReminderMessage()
			}
			return ""
		},
		ShouldStop: func() bool {
			if tracker.AllDone() {
				fmt.Println("\n  ✅ Все шаги плана выполнены!")
				return true
			}
			return false
		},
		OnError: func(err error) {
			fmt.Printf("\n❌ Ошибка API: %v\n", err)
		},
		OnPause: func(iteration int) {
			tracker.PrintProgress()
			fmt.Printf("\n⏸  Агент приостановлен на итерации %d\n", iteration+1)
			fmt.Println("   Введи /resume чтобы продолжить")
		},
		OnComplete: func() {
			tracker.PrintSummary()
		},
	}
}
