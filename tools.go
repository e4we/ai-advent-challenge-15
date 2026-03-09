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

// ExecuteTool запускает инструмент по имени
func ExecuteTool(name string, args map[string]interface{}) string {
	if args == nil {
		args = map[string]interface{}{}
	}
	switch name {
	case "run_command":
		cmd, _ := args["command"].(string)
		if cmd == "" {
			return "❌ Ошибка: не указана команда (command)"
		}
		return toolRunCommand(cmd)
	case "read_file":
		path, ok := args["path"].(string)
		if !ok || path == "" {
			return "❌ Ошибка: не указан путь файла (path)"
		}
		return toolReadFile(path)
	case "write_file":
		path, ok := args["path"].(string)
		if !ok || path == "" {
			return "❌ Ошибка: не указан путь файла (path)"
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
		return fmt.Sprintf("❌ Неизвестный инструмент: %s", name)
	}
}

func toolRunCommand(command string) string {
	fmt.Printf("  🔧 Выполняю: %s\n", command)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	output, err := cmd.CombinedOutput()

	if ctx.Err() == context.DeadlineExceeded {
		return "⏰ Команда превысила таймаут (30 сек)"
	}

	result := strings.TrimSpace(string(output))
	if err != nil && result == "" {
		return fmt.Sprintf("❌ Ошибка: %v", err)
	}
	if result == "" {
		return "(команда выполнена, вывод пуст)"
	}
	return result
}

func toolReadFile(path string) string {
	fmt.Printf("  📖 Читаю файл: %s\n", path)

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("❌ Ошибка чтения: %v", err)
	}

	content := string(data)
	if len(content) > 10000 {
		return content[:10000] + fmt.Sprintf("\n... (файл обрезан, всего %d символов)", len(content))
	}
	return content
}

func toolWriteFile(path, content string) string {
	fmt.Printf("  ✏️  Пишу файл: %s\n", path)

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Sprintf("❌ Ошибка создания директории: %v", err)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Sprintf("❌ Ошибка записи: %v", err)
	}
	return fmt.Sprintf("✅ Файл записан: %s (%d символов)", path, len(content))
}

func toolListFiles(directory string) string {
	fmt.Printf("  📁 Содержимое: %s\n", directory)

	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Sprintf("❌ Ошибка: %v", err)
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
		return "(пусто)"
	}
	return strings.Join(lines, "\n")
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
