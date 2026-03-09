package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// ─────────────────────────────────────────────
// TASK — задача как конечный автомат
//
// Task объединяет всё, что раньше было раскидано
// по отдельным переменным: userMessage, messages,
// plan, iteration, paused state.
//
// Состояния (State):
//
//   "plan"    → составляем план
//   "review"  → пользователь смотрит план
//   "execute" → выполняем через executor
//   "pause"   → приостановлено (можно продолжить)
//   "done"    → завершено
//   "fail"    → ошибка
//
// Переходы контролируются — нельзя перейти
// в состояние, которого нет в карте переходов.
// ─────────────────────────────────────────────

// Допустимые состояния
const (
	StatePlan    = "plan"
	StateReview  = "review"
	StateExecute = "execute"
	StatePause   = "pause"
	StateDone    = "done"
	StateFail    = "fail"
)

// Task — основная сущность агента.
type Task struct {
	ID        string    `json:"id"`
	Input     string    `json:"input"`     // исходный запрос пользователя
	State     string    `json:"state"`     // текущее состояние
	Plan      *Plan     `json:"plan"`      // план (может быть nil)
	Messages  []Message `json:"messages"`  // история диалога с LLM
	Iteration int       `json:"iteration"` // текущая итерация executor'а
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ─────────────────────────────────────────────
// КАРТА ДОПУСТИМЫХ ПЕРЕХОДОВ
// ─────────────────────────────────────────────
//
// Читается так: из состояния X можно перейти в [Y, Z].
// Любой другой переход — ошибка.

var transitions = map[string][]string{
	StatePlan:    {StateReview, StateFail},
	StateReview:  {StatePlan, StateExecute, StateDone}, // plan = edit, done = reject
	StateExecute: {StatePause, StateDone, StateFail},
	StatePause:   {StateExecute, StateDone},            // execute = resume, done = abort
	StateDone:    {},                                    // конечное состояние
	StateFail:    {},                                    // конечное состояние
}

// ─────────────────────────────────────────────
// СОЗДАНИЕ
// ─────────────────────────────────────────────

// NewTask создаёт новую задачу.
// Если PlanMode включён — начинает с "plan".
// Если нет — сразу "execute".
func NewTask(input string) *Task {
	initialState := StateExecute
	if config.PlanMode {
		initialState = StatePlan
	}

	return &Task{
		ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
		Input:     input,
		State:     initialState,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

// ─────────────────────────────────────────────
// ПЕРЕХОДЫ МЕЖДУ СОСТОЯНИЯМИ
// ─────────────────────────────────────────────

// TransitionTo пытается перевести задачу в новое состояние.
// Возвращает ошибку если переход недопустим.
func (t *Task) TransitionTo(newState string) error {
	allowed := transitions[t.State]

	for _, s := range allowed {
		if s == newState {
			t.State = newState
			t.UpdatedAt = time.Now()
			return nil
		}
	}

	return fmt.Errorf("переход %s → %s запрещён (допустимо: %v)", t.State, newState, allowed)
}

// Удобные методы для частых переходов.
// Каждый вызывает TransitionTo — нельзя обойти проверку.

func (t *Task) StartReview() error  { return t.TransitionTo(StateReview) }
func (t *Task) Replan() error       { return t.TransitionTo(StatePlan) }
func (t *Task) StartExecute() error { return t.TransitionTo(StateExecute) }
func (t *Task) Pause() error        { return t.TransitionTo(StatePause) }
func (t *Task) Complete() error     { return t.TransitionTo(StateDone) }
func (t *Task) Fail() error         { return t.TransitionTo(StateFail) }

func (t *Task) Resume() error {
	return t.TransitionTo(StateExecute)
}

func (t *Task) Reject() error {
	return t.TransitionTo(StateDone)
}

// IsTerminal возвращает true если задача завершена (done или fail)
func (t *Task) IsTerminal() bool {
	return t.State == StateDone || t.State == StateFail
}

// ─────────────────────────────────────────────
// СОХРАНЕНИЕ НА ДИСК
// ─────────────────────────────────────────────

const taskFilePath = ".agent-state.json"

// Save сохраняет задачу на диск.
// Вызывается после каждого изменения состояния.
func (t *Task) Save() {
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		fmt.Printf("  ⚠️  Не удалось сериализовать задачу: %v\n", err)
		return
	}

	if err := os.WriteFile(taskFilePath, data, 0644); err != nil {
		fmt.Printf("  ⚠️  Не удалось сохранить на диск: %v\n", err)
		return
	}

	fmt.Printf("  💾 Задача сохранена в %s (состояние: %s)\n", taskFilePath, t.State)
}

// LoadTask загружает задачу с диска. Возвращает nil если файла нет.
func LoadTask() *Task {
	data, err := os.ReadFile(taskFilePath)
	if err != nil {
		return nil
	}

	var task Task
	if err := json.Unmarshal(data, &task); err != nil {
		fmt.Printf("  ⚠️  Файл %s повреждён: %v\n", taskFilePath, err)
		return nil
	}

	return &task
}

// DeleteTaskFile удаляет файл состояния с диска.
func DeleteTaskFile() {
	os.Remove(taskFilePath)
}

// ─────────────────────────────────────────────
// ОТОБРАЖЕНИЕ
// ─────────────────────────────────────────────

// StateIcon возвращает иконку для текущего состояния
func (t *Task) StateIcon() string {
	switch t.State {
	case StatePlan:
		return "📋"
	case StateReview:
		return "👀"
	case StateExecute:
		return "🚀"
	case StatePause:
		return "⏸"
	case StateDone:
		return "✅"
	case StateFail:
		return "❌"
	default:
		return "❓"
	}
}

// PrintStatus выводит информацию о задаче
func (t *Task) PrintStatus() {
	fmt.Printf("\n%s Задача [%s]\n", t.StateIcon(), t.State)
	fmt.Printf("   Запрос: %s\n", truncateStr(t.Input, 60))
	fmt.Printf("   Итерация: %d, сообщений: %d\n", t.Iteration, len(t.Messages))
	if t.Plan != nil {
		fmt.Printf("   План: %s (%d шагов)\n", t.Plan.Summary, len(t.Plan.Steps))
	}
	fmt.Printf("   Создана: %s\n", t.CreatedAt.Format("15:04:05"))
}
