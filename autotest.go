package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// AutoTestSpec — спецификация автотеста для проверки артефакта.
type AutoTestSpec struct {
	Label   string // "go build", "go test ./...", "python syntax"
	Command string // команда для bash (пустая = встроенная проверка)
}

// GetAutoTest определяет, нужен ли автотест для данного tool call.
// Возвращает nil, если автотест не нужен.
func GetAutoTest(call ToolCall, result ToolResult) *AutoTestSpec {
	if result.IsError {
		return nil
	}
	if call.Name != "write_file" {
		return nil
	}

	path, _ := call.Arguments["path"].(string)
	if path == "" {
		return nil
	}

	// JSON — особый случай: встроенная проверка без внешней команды
	if strings.HasSuffix(path, ".json") {
		return &AutoTestSpec{Label: "json syntax"}
	}

	return matchAutoTest(path)
}

// matchAutoTest определяет автотест по расширению/имени файла.
func matchAutoTest(filePath string) *AutoTestSpec {
	base := filepath.Base(filePath)
	dir := filepath.Dir(filePath)
	quotedDir := shellQuote(dir)

	// _test.go — приоритет выше чем .go
	if strings.HasSuffix(base, "_test.go") {
		return &AutoTestSpec{
			Label:   "go test",
			Command: fmt.Sprintf("cd %s && go test ./...", quotedDir),
		}
	}

	if strings.HasSuffix(base, ".go") {
		return &AutoTestSpec{
			Label:   "go build",
			Command: fmt.Sprintf("cd %s && go build .", quotedDir),
		}
	}

	if strings.HasSuffix(base, ".py") {
		if _, err := exec.LookPath("python3"); err != nil {
			if _, err := exec.LookPath("python"); err != nil {
				return nil
			}
			return &AutoTestSpec{
				Label:   "python syntax",
				Command: fmt.Sprintf("python -c \"import ast; ast.parse(open(%s).read())\"", shellQuote(filePath)),
			}
		}
		return &AutoTestSpec{
			Label:   "python syntax",
			Command: fmt.Sprintf("python3 -c \"import ast; ast.parse(open(%s).read())\"", shellQuote(filePath)),
		}
	}

	if base == "Dockerfile" || strings.HasPrefix(base, "Dockerfile.") {
		if _, err := exec.LookPath("docker"); err != nil {
			return nil
		}
		return &AutoTestSpec{
			Label:   "docker build --check",
			Command: fmt.Sprintf("cd %s && docker build --check .", quotedDir),
		}
	}

	return nil
}

// RunAutoTest выполняет автотест. Для JSON — встроенная проверка,
// для остального — через toolRunCommand.
func RunAutoTest(spec *AutoTestSpec, call ToolCall) ToolResult {
	// JSON — встроенная проверка
	if spec.Label == "json syntax" {
		content, _ := call.Arguments["content"].(string)
		if json.Valid([]byte(content)) {
			return ToolResult{Output: "json syntax", IsError: false}
		}
		return ToolResult{Output: "invalid JSON syntax", IsError: true}
	}

	return toolRunCommand(spec.Command)
}

// autotestHook возвращает функцию-хук для AfterToolResult.
func autotestHook() func(call ToolCall, result ToolResult) *ToolResult {
	return func(call ToolCall, result ToolResult) *ToolResult {
		spec := GetAutoTest(call, result)
		if spec == nil {
			return nil
		}
		fmt.Printf("\n  🧪 Автотест: %s\n", spec.Label)
		autoResult := RunAutoTest(spec, call)
		if autoResult.IsError {
			fmt.Printf("  ❌ %s\n", truncateStr(autoResult.Output, 200))
		} else {
			fmt.Printf("  ✅ %s\n", spec.Label)
		}
		return &autoResult
	}
}

// shellQuote экранирует строку для безопасного использования в bash.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
