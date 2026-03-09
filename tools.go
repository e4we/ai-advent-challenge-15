package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ─────────────────────────────────────────────
// ИНСТРУМЕНТЫ — то, что агент умеет делать
// ─────────────────────────────────────────────

// ToolResult — результат выполнения инструмента с флагом ошибки.
type ToolResult struct {
	Output  string
	IsError bool
}

// ExecuteTool запускает инструмент по имени
func ExecuteTool(name string, args map[string]interface{}) ToolResult {
	if args == nil {
		args = map[string]interface{}{}
	}
	switch name {
	case "run_command":
		cmd, _ := args["command"].(string)
		if cmd == "" {
			return ToolResult{Output: "❌ Ошибка: не указана команда (command)", IsError: true}
		}
		return toolRunCommand(cmd)
	case "read_file":
		path, ok := args["path"].(string)
		if !ok || path == "" {
			return ToolResult{Output: "❌ Ошибка: не указан путь файла (path)", IsError: true}
		}
		return toolReadFile(path)
	case "write_file":
		path, ok := args["path"].(string)
		if !ok || path == "" {
			return ToolResult{Output: "❌ Ошибка: не указан путь файла (path)", IsError: true}
		}
		content, _ := args["content"].(string)
		return toolWriteFile(path, content)
	case "list_files":
		dir, ok := args["directory"].(string)
		if !ok || dir == "" {
			dir = "."
		}
		return toolListFiles(dir)
	default:
		return ToolResult{Output: fmt.Sprintf("❌ Неизвестный инструмент: %s", name), IsError: true}
	}
}

func toolRunCommand(command string) ToolResult {
	fmt.Printf("  🔧 Выполняю: %s\n", command)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	output, err := cmd.CombinedOutput()

	if ctx.Err() == context.DeadlineExceeded {
		return ToolResult{Output: "⏰ Команда превысила таймаут (30 сек)", IsError: true}
	}

	result := strings.TrimSpace(string(output))
	if err != nil && result == "" {
		return ToolResult{Output: fmt.Sprintf("❌ Ошибка: %v", err), IsError: true}
	}
	if err != nil {
		return ToolResult{Output: result, IsError: true}
	}
	if result == "" {
		return ToolResult{Output: "(команда выполнена, вывод пуст)", IsError: false}
	}
	return ToolResult{Output: result, IsError: false}
}

func toolReadFile(path string) ToolResult {
	fmt.Printf("  📖 Читаю файл: %s\n", path)

	data, err := os.ReadFile(path)
	if err != nil {
		return ToolResult{Output: fmt.Sprintf("❌ Ошибка чтения: %v", err), IsError: true}
	}

	content := string(data)
	if len(content) > 10000 {
		content = content[:10000] + fmt.Sprintf("\n... (файл обрезан, всего %d символов)", len(content))
	}
	return ToolResult{Output: content, IsError: false}
}

func toolWriteFile(path, content string) ToolResult {
	fmt.Printf("  ✏️  Пишу файл: %s\n", path)

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return ToolResult{Output: fmt.Sprintf("❌ Ошибка создания директории: %v", err), IsError: true}
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return ToolResult{Output: fmt.Sprintf("❌ Ошибка записи: %v", err), IsError: true}
	}
	return ToolResult{Output: fmt.Sprintf("✅ Файл записан: %s (%d символов)", path, len(content)), IsError: false}
}

func toolListFiles(directory string) ToolResult {
	fmt.Printf("  📁 Содержимое: %s\n", directory)

	entries, err := os.ReadDir(directory)
	if err != nil {
		return ToolResult{Output: fmt.Sprintf("❌ Ошибка: %v", err), IsError: true}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	var lines []string
	limit := 50
	for i, entry := range entries {
		if i >= limit {
			lines = append(lines, fmt.Sprintf("  ... и ещё %d", len(entries)-limit))
			break
		}
		icon := "📄"
		if entry.IsDir() {
			icon = "📁"
		}
		lines = append(lines, fmt.Sprintf("  %s %s", icon, entry.Name()))
	}

	if len(lines) == 0 {
		return ToolResult{Output: "(пусто)", IsError: false}
	}
	return ToolResult{Output: strings.Join(lines, "\n"), IsError: false}
}

// ValidateToolResult проверяет результат tool call по эвристикам.
// Не меняет Output — только ставит флаг IsError.
func ValidateToolResult(toolName string, result ToolResult) ToolResult {
	if result.IsError {
		return result
	}

	if toolName == "run_command" {
		lower := strings.ToLower(result.Output)
		errorPatterns := []string{
			"permission denied",
			"command not found",
			"no such file or directory",
		}
		for _, pattern := range errorPatterns {
			if strings.Contains(lower, pattern) {
				result.IsError = true
				return result
			}
		}
	}

	return result
}

// GetToolNames возвращает имена всех зарегистрированных инструментов
func GetToolNames() []string {
	schemas := GetToolSchemas()
	names := make([]string, len(schemas))
	for i, s := range schemas {
		names[i] = s.Name
	}
	return names
}

// ─────────────────────────────────────────────
// Описания инструментов для LLM (JSON Schema)
// ─────────────────────────────────────────────

// ToolSchema — описание одного инструмента
type ToolSchema struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// GetToolSchemas возвращает описания всех инструментов
func GetToolSchemas() []ToolSchema {
	return []ToolSchema{
		{
			Name:        "run_command",
			Description: "Выполнить bash-команду в терминале. Используй для установки пакетов, запуска скриптов, git и т.д.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command": map[string]interface{}{
						"type":        "string",
						"description": "Bash-команда для выполнения",
					},
				},
				"required": []string{"command"},
			},
		},
		{
			Name:        "read_file",
			Description: "Прочитать содержимое файла.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Путь к файлу",
					},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "write_file",
			Description: "Создать или перезаписать файл.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Путь к файлу",
					},
					"content": map[string]interface{}{
						"type":        "string",
						"description": "Содержимое файла",
					},
				},
				"required": []string{"path", "content"},
			},
		},
		{
			Name:        "list_files",
			Description: "Показать список файлов и папок в директории.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"directory": map[string]interface{}{
						"type":        "string",
						"description": "Путь к директории",
						"default":     ".",
					},
				},
				"required": []string{},
			},
		},
	}
}
