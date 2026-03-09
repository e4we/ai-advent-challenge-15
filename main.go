package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
)

// ─────────────────────────────────────────────
// СИСТЕМНЫЙ ПРОМПТ
// ─────────────────────────────────────────────

const systemPrompt = `Ты — CLI-агент, помощник разработчика. Ты работаешь в терминале пользователя.

Твои возможности:
- Выполнять bash-команды (run_command)
- Читать файлы (read_file)
- Создавать и редактировать файлы (write_file)
- Просматривать структуру директорий (list_files)

Правила:
1. Сначала разберись в задаче, потом действуй
2. Если нужно — сначала прочитай существующие файлы
3. Объясняй что делаешь кратко, но понятно
4. Если команда может быть опасной — предупреди
5. Всегда отвечай на русском языке`

const planningPrompt = `Ты — CLI-агент в режиме ПЛАНИРОВАНИЯ.

Пользователь дал тебе задачу. Твоя цель — составить ПЛАН выполнения,
НЕ выполняя никаких действий.

Ответь строго в формате JSON (без маркдауна, без комментариев):
{
  "summary": "Краткое описание задачи (1 предложение)",
  "steps": [
    {
      "number": 1,
      "action": "Что конкретно сделать",
      "reason": "Зачем это нужно"
    }
  ],
  "risks": ["Возможный риск 1"]
}

Правила:
- Каждый шаг = одно конкретное действие
- Шаги идут в логическом порядке
- Простая задача = 1-2 шага, сложная = до 10
- Начинай с изучения проекта (list_files, read_file), если нужен контекст
- Опиши риски, если есть деструктивные операции`

// ─────────────────────────────────────────────
// ФАБРИКА ПРОВАЙДЕРОВ
// ─────────────────────────────────────────────

func getProvider() Provider {
	switch config.Provider {
	case "anthropic":
		return NewAnthropicProvider()
	case "openai", "local":
		return NewOpenAIProvider()
	default:
		fmt.Printf("❌ Неизвестный провайдер: %s\n", config.Provider)
		os.Exit(1)
		return nil
	}
}

// ─────────────────────────────────────────────
// СТРУКТУРА ПЛАНА
// ─────────────────────────────────────────────

type Plan struct {
	Summary string     `json:"summary"`
	Steps   []PlanStep `json:"steps"`
	Risks   []string   `json:"risks"`
}

type PlanStep struct {
	Number int    `json:"number"`
	Action string `json:"action"`
	Reason string `json:"reason"`
	Status string `json:"status,omitempty"` // "pending", "done", "skipped"
}

// ─────────────────────────────────────────────
// ОТСЛЕЖИВАНИЕ ПРОГРЕССА ПЛАНА
// ─────────────────────────────────────────────

type PlanTracker struct {
	Plan          *Plan
	CompletedStep int
}

func NewPlanTracker(plan *Plan) *PlanTracker {
	for i := range plan.Steps {
		plan.Steps[i].Status = "pending"
	}
	return &PlanTracker{Plan: plan, CompletedStep: 0}
}

func (t *PlanTracker) UpdateFromText(text string) bool {
	updated := false
	lower := strings.ToLower(text)

	for i := range t.Plan.Steps {
		step := &t.Plan.Steps[i]
		if step.Status == "done" {
			continue
		}

		num := fmt.Sprintf("%d", step.Number)
		markers := []string{
			"шаг " + num,
			"step " + num,
			"шаг " + num + ":",
			"шаг " + num + " —",
			"шаг " + num + " -",
			"шаг " + num + ".",
		}

		for _, marker := range markers {
			if strings.Contains(lower, marker) {
				step.Status = "done"
				if step.Number > t.CompletedStep {
					t.CompletedStep = step.Number
				}
				updated = true
				break
			}
		}
	}

	return updated
}

func (t *PlanTracker) AllDone() bool {
	for _, step := range t.Plan.Steps {
		if step.Status != "done" {
			return false
		}
	}
	return true
}

func (t *PlanTracker) DoneCount() int {
	count := 0
	for _, step := range t.Plan.Steps {
		if step.Status == "done" {
			count++
		}
	}
	return count
}

func (t *PlanTracker) PendingSteps() []PlanStep {
	var pending []PlanStep
	for _, step := range t.Plan.Steps {
		if step.Status != "done" {
			pending = append(pending, step)
		}
	}
	return pending
}

func (t *PlanTracker) PrintProgress() {
	total := len(t.Plan.Steps)
	done := t.DoneCount()
	barLen := 20
	filled := 0
	if total > 0 {
		filled = (done * barLen) / total
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barLen-filled)
	fmt.Printf("\n  📊 Прогресс: [%s] %d/%d шагов\n", bar, done, total)
}

func (t *PlanTracker) PrintSummary() {
	total := len(t.Plan.Steps)
	done := t.DoneCount()

	fmt.Printf("\n╔══ 📊 ИТОГ ═══════════════════════════════════╗\n")
	fmt.Printf("║  %s\n", t.Plan.Summary)
	fmt.Printf("╠══════════════════════════════════════════════╣\n")

	for _, step := range t.Plan.Steps {
		icon := "⬜"
		switch step.Status {
		case "done":
			icon = "✅"
		case "skipped":
			icon = "⏭️"
		}
		fmt.Printf("║  %s %d. %s\n", icon, step.Number, step.Action)
	}

	fmt.Printf("╠══════════════════════════════════════════════╣\n")
	if done == total {
		fmt.Printf("║  ✅ Все %d шагов выполнены!\n", total)
	} else {
		fmt.Printf("║  ⚠️  Выполнено %d из %d шагов\n", done, total)
	}
	fmt.Printf("╚══════════════════════════════════════════════╝\n")
}

func (t *PlanTracker) ReminderMessage() string {
	pending := t.PendingSteps()
	if len(pending) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("Ты ещё не выполнил следующие шаги из плана:\n")
	for _, step := range pending {
		fmt.Fprintf(&b, "- Шаг %d: %s\n", step.Number, step.Action)
	}
	b.WriteString("Продолжай выполнение. Перед каждым шагом пиши \"Шаг N: ...\"")
	return b.String()
}

// ─────────────────────────────────────────────
// ПАУЗА (Ctrl+C → флаг)
// ─────────────────────────────────────────────

var (
	pauseSignal bool
	pauseMu     sync.Mutex
)

func isPaused() bool {
	pauseMu.Lock()
	defer pauseMu.Unlock()
	return pauseSignal
}

func requestPause() {
	pauseMu.Lock()
	defer pauseMu.Unlock()
	pauseSignal = true
}

func clearPause() {
	pauseMu.Lock()
	defer pauseMu.Unlock()
	pauseSignal = false
}

func setupPauseHandler() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT)
	go func() {
		for range sigChan {
			requestPause()
			fmt.Printf("\n\n⏸  Пауза запрошена... (завершаю текущий шаг)\n")
		}
	}()
}

// ─────────────────────────────────────────────
// ПОШАГОВЫЙ РЕЖИМ
// ─────────────────────────────────────────────

func stepCheck(iteration int) string {
	if !config.StepMode {
		return "continue"
	}
	fmt.Printf("\n  ⏯  Шаг %d выполнен. [enter=далее / p=пауза / a=прервать]: ", iteration+1)
	answer := strings.ToLower(askString())
	switch answer {
	case "p", "pause", "пауза":
		return "pause"
	case "a", "abort", "стоп":
		return "abort"
	default:
		return "continue"
	}
}

// ─────────────────────────────────────────────
// ПЛАНИРОВАНИЕ
// ─────────────────────────────────────────────

// doPlan составляет план и проводит через ревью.
// Обновляет task.State через конечный автомат.
func doPlan(task *Task, provider Provider) {
	currentInput := task.Input

	for {
		// State: plan → составляем план
		fmt.Println("\n📋 Составляю план...")

		wrappedMessage := planningPrompt + "\n\nЗадача пользователя:\n" + currentInput
		planMessages := []Message{
			{Role: "user", Content: wrappedMessage},
		}

		response, err := provider.Chat(planMessages)
		if err != nil {
			fmt.Printf("❌ Ошибка при планировании: %v\n", err)
			task.Fail()
			task.Save()
			return
		}

		plan := parsePlan(response.Text)
		if plan == nil {
			// Не удалось распарсить — выполняем без плана
			fmt.Printf("\n🤖 %s\n", response.Text)
			fmt.Print("\n▶ Выполнить задачу без плана? [y/N]: ")
			if askYesNo() {
				task.Plan = nil
				task.StartExecute()
				task.Save()
			} else {
				task.Reject()
				task.Save()
			}
			return
		}

		task.Plan = plan
		task.StartReview() // plan → review
		task.Save()

		// State: review → показываем пользователю
		printPlan(plan)
		fmt.Print("\n▶ Выполнить план? [y/N/edit]: ")
		answer := askString()

		switch strings.ToLower(answer) {
		case "y", "yes", "д", "да":
			task.StartExecute() // review → execute
			task.Save()
			return

		case "edit", "e", "ред":
			fmt.Print("✏️  Уточнение: ")
			edit := askString()
			if edit != "" {
				currentInput = task.Input + "\n\nУточнение: " + edit
				task.Replan() // review → plan
				task.Save()
				continue
			}
			task.StartExecute() // review → execute
			task.Save()
			return

		default:
			fmt.Println("⏭  План отклонён")
			task.Reject() // review → done
			task.Save()
			return
		}
	}
}

// doExecute запускает выполнение задачи через Executor.
func doExecute(task *Task, provider Provider) {
	// Подготавливаем сообщение для LLM
	if len(task.Messages) == 0 || task.Iteration == 0 {
		userMsg := task.Input
		if task.Plan != nil {
			userMsg = buildPlanPrompt(task)
		}
		task.Messages = append(task.Messages, Message{
			Role:    "user",
			Content: userMsg,
		})
	}

	// Выбираем хуки
	var hooks ExecHooks
	if task.Plan != nil {
		tracker := NewPlanTracker(task.Plan)
		hooks = TrackedHooks(tracker, 2)
	} else {
		hooks = DefaultHooks()
	}

	// Запускаем executor — он обновляет task напрямую
	Execute(task, provider, hooks)
}

// doResume возобновляет задачу после паузы.
func doResume(task *Task, provider Provider) {
	fmt.Printf("\n▶️  Возобновляю выполнение (итерация %d)...\n", task.Iteration+1)

	task.Resume() // pause → execute
	task.Save()

	// Добавляем подсказку LLM
	task.Messages = append(task.Messages, Message{
		Role:    "user",
		Content: "Продолжай выполнение с того места, где остановился.",
	})

	doExecute(task, provider)
}

// buildPlanPrompt формирует промпт с планом для LLM
func buildPlanPrompt(task *Task) string {
	var b strings.Builder
	b.WriteString(task.Input)
	b.WriteString("\n\n--- УТВЕРЖДЁННЫЙ ПЛАН (следуй ему по шагам) ---\n")
	for _, step := range task.Plan.Steps {
		fmt.Fprintf(&b, "%d. %s\n", step.Number, step.Action)
	}
	b.WriteString("---\n")
	b.WriteString("Выполняй шаги строго по порядку.\n")
	b.WriteString("Перед каждым шагом ОБЯЗАТЕЛЬНО пиши: \"Шаг N: ...\" (это нужно для отслеживания прогресса).\n")
	b.WriteString("После завершения всех шагов — подведи итог.")
	return b.String()
}

func parsePlan(text string) *Plan {
	jsonStr := text

	if idx := strings.Index(jsonStr, "```json"); idx >= 0 {
		jsonStr = jsonStr[idx+7:]
		if end := strings.Index(jsonStr, "```"); end >= 0 {
			jsonStr = jsonStr[:end]
		}
	} else if idx := strings.Index(jsonStr, "```"); idx >= 0 {
		jsonStr = jsonStr[idx+3:]
		if end := strings.Index(jsonStr, "```"); end >= 0 {
			jsonStr = jsonStr[:end]
		}
	}

	start := strings.Index(jsonStr, "{")
	end := strings.LastIndex(jsonStr, "}")
	if start >= 0 && end > start {
		jsonStr = jsonStr[start : end+1]
	}

	var plan Plan
	if err := json.Unmarshal([]byte(jsonStr), &plan); err != nil {
		return nil
	}
	if len(plan.Steps) == 0 {
		return nil
	}
	return &plan
}

func printPlan(plan *Plan) {
	fmt.Printf("\n╔══ 📋 ПЛАН ══════════════════════════════════╗\n")
	fmt.Printf("║  %s\n", plan.Summary)
	fmt.Printf("╠══════════════════════════════════════════════╣\n")

	for _, step := range plan.Steps {
		fmt.Printf("║  %d. %s\n", step.Number, step.Action)
		if step.Reason != "" {
			fmt.Printf("║     └─ %s\n", step.Reason)
		}
	}

	if len(plan.Risks) > 0 {
		fmt.Printf("╠══════════════════════════════════════════════╣\n")
		fmt.Printf("║  ⚠️  Риски:\n")
		for _, risk := range plan.Risks {
			fmt.Printf("║  • %s\n", risk)
		}
	}

	fmt.Printf("╚══════════════════════════════════════════════╝\n")
}

// ─────────────────────────────────────────────
// ОБРАБОТКА ЗАДАЧИ — единая точка входа
// ─────────────────────────────────────────────

// runTask обрабатывает задачу от начала до конца.
// Проверяет текущий State и выполняет нужное действие.
func runTask(task *Task, provider Provider) {
	switch task.State {
	case StatePlan:
		doPlan(task, provider)
		// После планирования задача может быть в execute, done или fail
		if task.State == StateExecute {
			doExecute(task, provider)
		}

	case StateExecute:
		doExecute(task, provider)

	case StatePause:
		doResume(task, provider)

	case StateDone, StateFail:
		// Уже завершена, ничего не делаем
	}
}

// ─────────────────────────────────────────────
// ВВОД С КЛАВИАТУРЫ
// ─────────────────────────────────────────────

var stdinScanner *bufio.Scanner

func initScanner() {
	if stdinScanner == nil {
		stdinScanner = bufio.NewScanner(os.Stdin)
		stdinScanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	}
}

func askString() string {
	initScanner()
	if stdinScanner.Scan() {
		return strings.TrimSpace(stdinScanner.Text())
	}
	return ""
}

func askYesNo() bool {
	a := strings.ToLower(askString())
	return a == "y" || a == "yes" || a == "д" || a == "да"
}

// ─────────────────────────────────────────────
// ТОЧКА ВХОДА
// ─────────────────────────────────────────────

func main() {
	planState := "выкл"
	if config.PlanMode {
		planState = "вкл"
	}
	stepState := "выкл"
	if config.StepMode {
		stepState = "вкл"
	}
	chatState := "выкл"
	if config.ChatMode {
		chatState = "вкл"
	}

	fmt.Println("╔══════════════════════════════════════╗")
	fmt.Printf("║  🤖 Мой CLI-агент (Go)               ║\n")
	fmt.Printf("║  Провайдер: %-25s║\n", config.Provider)
	fmt.Printf("║  Модель: %-28s║\n", config.Model)
	fmt.Printf("║  Планирование: %-22s║\n", planState)
	fmt.Printf("║  Пошаговый: %-25s║\n", stepState)
	fmt.Printf("║  Чат-режим: %-25s║\n", chatState)
	fmt.Println("║  Ctrl+C = пауза (не выход!)         ║")
	fmt.Println("║  Команды: /help для списка            ║")
	fmt.Println("╚══════════════════════════════════════╝")

	setupPauseHandler()

	provider := getProvider()
	var currentTask *Task
	var chatHistory []Message
	initScanner()

	// Загружаем задачу с диска если есть
	if saved := LoadTask(); saved != nil && !saved.IsTerminal() {
		currentTask = saved
		fmt.Printf("\n💾 Найдена незавершённая задача с прошлого запуска!\n")
		currentTask.PrintStatus()
		fmt.Println("   Введи /resume для продолжения или /abort для отмены")
	}

	// Режим одной команды
	if len(os.Args) > 1 {
		userInput := strings.Join(os.Args[1:], " ")
		task := NewTask(userInput)
		runTask(task, provider)
		if task.IsTerminal() {
			DeleteTaskFile()
		}
		return
	}

	// Интерактивный REPL
	for {
		prompt := "\n👤 Вы: "
		if config.ChatMode {
			prompt = "\n💬 Вы: "
		} else if currentTask != nil && currentTask.State == StatePause {
			prompt = "\n👤 Вы (⏸ есть пауза — введи /resume): "
		}
		fmt.Print(prompt)

		if !stdinScanner.Scan() {
			fmt.Println("\n👋 Пока!")
			break
		}

		userInput := strings.TrimSpace(stdinScanner.Text())
		if userInput == "" {
			continue
		}

		switch strings.ToLower(userInput) {
		case "/exit":
			fmt.Println("👋 Пока!")
			return

		case "/clear":
			currentTask = nil
			chatHistory = nil
			DeleteTaskFile()
			fmt.Println("🧹 История очищена")
			continue

		case "/plan":
			config.PlanMode = !config.PlanMode
			state := "выключен ⚡"
			if config.PlanMode {
				state = "включён 📋"
			}
			fmt.Printf("📋 Режим планирования: %s\n", state)
			continue

		case "/step":
			config.StepMode = !config.StepMode
			state := "выключен ⚡"
			if config.StepMode {
				state = "включён 👣"
			}
			fmt.Printf("👣 Пошаговый режим: %s\n", state)
			continue

		case "/chat":
			config.ChatMode = !config.ChatMode
			state := "выключен 🤖"
			if config.ChatMode {
				state = "включён 💬"
				chatHistory = nil
			}
			fmt.Printf("💬 Чат-режим: %s\n", state)
			continue

		case "/resume":
			if currentTask == nil || currentTask.State != StatePause {
				fmt.Println("❌ Нечего возобновлять")
				continue
			}
			doResume(currentTask, provider)
			if currentTask.IsTerminal() {
				DeleteTaskFile()
				currentTask = nil
			}
			continue

		case "/abort":
			if currentTask == nil || currentTask.IsTerminal() {
				fmt.Println("❌ Нечего прерывать")
				continue
			}
			fmt.Println("🛑 Задача отменена")
			currentTask.Complete()
			DeleteTaskFile()
			currentTask = nil
			continue

		case "/status":
			if currentTask != nil && !currentTask.IsTerminal() {
				currentTask.PrintStatus()
			} else {
				fmt.Println("✅ Нет активных задач")
			}
			fmt.Printf("📋 План: %v | 👣 Шаги: %v | 💬 Чат: %v\n", config.PlanMode, config.StepMode, config.ChatMode)
			continue

		case "/help":
			fmt.Println(`
📖 Доступные команды:
  /exit    — выйти
  /clear   — очистить историю
  /plan    — вкл/выкл режим планирования
  /step    — вкл/выкл пошаговый режим
  /chat    — вкл/выкл чат-режим (без инструментов)
  /resume  — продолжить после паузы
  /abort   — отменить текущую задачу
  /status  — показать состояние задачи
  /help    — эта справка

⏸ Пауза:
  Ctrl+C   — приостановить (сохраняется на диск)
  /resume  — продолжить (даже после перезапуска!)
  /abort   — отменить

👣 Пошаговый режим:
  enter   — следующий шаг
  p       — пауза
  a       — прервать

📋 Планирование:
  y / n / edit — утвердить / отклонить / уточнить`)
			continue
		}

		// ── Чат-режим или новая задача ──
		if config.ChatMode {
			chatHistory = append(chatHistory, Message{Role: "user", Content: userInput})
			fmt.Println("\n🤖 ...")
			response, err := provider.Chat(chatHistory)
			if err != nil {
				fmt.Printf("❌ Ошибка: %v\n", err)
				chatHistory = chatHistory[:len(chatHistory)-1]
				continue
			}
			fmt.Printf("\n🤖 %s\n", response.Text)
			chatHistory = append(chatHistory, Message{Role: "assistant", Content: response.Text})
			continue
		}

		task := NewTask(userInput)
		currentTask = task
		runTask(task, provider)

		if task.IsTerminal() {
			DeleteTaskFile()
			currentTask = nil
		}
	}
}

func truncateStr(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
