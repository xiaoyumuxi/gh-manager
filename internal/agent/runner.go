package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// 只读模式默认允许的 Claude 工具：读取代码与只读 git 命令。
var claudeReadTools = []string{
	"Read", "Grep", "Glob", "Skill", "TodoWrite", "Task",
	"Bash(git diff:*)", "Bash(git log:*)", "Bash(git show:*)", "Bash(git status:*)",
	"Bash(git blame:*)", "Bash(git ls-files:*)", "Bash(git grep:*)", "Bash(git rev-parse:*)",
	"Bash(ls:*)", "Bash(cat:*)", "Bash(head:*)", "Bash(tail:*)", "Bash(wc:*)", "Bash(find:*)",
}

var claudeEditTools = []string{"Edit", "MultiEdit", "Write", "NotebookEdit"}

// 无论什么模式都禁止：Agent 不能直接写 GitHub，所有写回必须经过用户在网页中确认。
var claudeDeniedTools = []string{
	"Bash(gh:*)", "Bash(git push:*)", "Bash(git remote:*)", "Bash(git config:*)",
	"Bash(git commit:*)", "Bash(git reset:*)", "Bash(git checkout:*)", "Bash(git switch:*)",
	"Bash(curl:*)", "Bash(wget:*)",
}

// Invocation 是一次 Agent 调用所需的全部参数。
type Invocation struct {
	Agent        string
	Mode         string
	Command      string
	Model        string
	Prompt       string
	Dir          string
	AllowedTools string
	LastMessage  string
	AddDirs      []string
	Timeout      time.Duration
}

// Outcome 是解析输出流后得到的最终结果。
type Outcome struct {
	SessionID  string
	Result     string
	CostUSD    float64
	DurationMS int64
	IsError    bool
	ErrorText  string
}

// Event 是推送给网页的标准化日志事件。
type Event struct {
	Seq  int    `json:"seq"`
	Time int64  `json:"t"`
	Type string `json:"type"`
	Text string `json:"text"`
	Tool string `json:"tool,omitempty"`
}

func buildCommand(ctx context.Context, inv Invocation) (*exec.Cmd, error) {
	var args []string
	switch inv.Agent {
	case "claude":
		allowed := append([]string{}, claudeReadTools...)
		permission := "default"
		if inv.Mode == "edit" {
			allowed = append(allowed, claudeEditTools...)
			permission = "acceptEdits"
		}
		allowed = append(allowed, splitTools(inv.AllowedTools)...)
		args = []string{
			"-p", "--output-format", "stream-json", "--verbose",
			// 只加载用户级配置：PR 中的 .claude/settings.json、hooks、MCP 配置来自外部，不应被执行。
			"--setting-sources", "user",
			"--strict-mcp-config",
			"--permission-mode", permission,
			"--allowedTools", strings.Join(allowed, ","),
			"--disallowedTools", strings.Join(claudeDeniedTools, ","),
		}
		if inv.Model != "" {
			args = append(args, "--model", inv.Model)
		}
		for _, dir := range inv.AddDirs {
			args = append(args, "--add-dir", dir)
		}
		args = append(args, "--", inv.Prompt)
	case "codex":
		sandbox := "read-only"
		if inv.Mode == "edit" {
			sandbox = "workspace-write"
		}
		args = []string{
			"exec", "--json", "--color", "never",
			"-s", sandbox,
			"-C", inv.Dir,
			"--skip-git-repo-check",
		}
		if inv.LastMessage != "" {
			args = append(args, "-o", inv.LastMessage)
		}
		if inv.Model != "" {
			args = append(args, "-m", inv.Model)
		}
		args = append(args, "--", inv.Prompt)
	default:
		return nil, fmt.Errorf("不支持的 Agent: %s", inv.Agent)
	}
	cmd := exec.CommandContext(ctx, inv.Command, args...)
	cmd.Dir = inv.Dir
	cmd.Env = agentEnv()
	setProcessGroup(cmd)
	cmd.Cancel = func() error { return terminateProcess(cmd) }
	cmd.WaitDelay = 10 * time.Second
	return cmd, nil
}

func agentEnv() []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		key := entry
		if index := strings.IndexByte(entry, '='); index >= 0 {
			key = entry[:index]
		}
		// 避免被识别为嵌套在另一个 Claude Code 会话中运行。
		if key == "CLAUDECODE" || key == "CLAUDE_CODE_ENTRYPOINT" || key == "CLAUDE_CODE_SSE_PORT" {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "GIT_TERMINAL_PROMPT=0", "NO_COLOR=1")
}

func splitTools(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '\n' })
	tools := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			tools = append(tools, part)
		}
	}
	return tools
}

// execute 运行 Agent，把原始输出写入 raw，并通过 emit 推送标准化事件。
func execute(ctx context.Context, inv Invocation, raw io.Writer, emit func(Event), onSession func(string)) (Outcome, error) {
	runCtx, cancel := context.WithTimeout(ctx, inv.Timeout)
	defer cancel()
	cmd, err := buildCommand(runCtx, inv)
	if err != nil {
		return Outcome{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Outcome{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return Outcome{}, err
	}
	started := time.Now()
	if err := cmd.Start(); err != nil {
		return Outcome{}, fmt.Errorf("启动 %s 失败: %w", inv.Agent, err)
	}

	stderrDone := make(chan string, 1)
	go func() {
		var tail []string
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 64<<10), 1<<20)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "Reading additional input from stdin") {
				continue
			}
			tail = append(tail, line)
			if len(tail) > 20 {
				tail = tail[1:]
			}
			emit(Event{Type: "stderr", Text: truncate(line, 2000)})
		}
		stderrDone <- strings.Join(tail, "\n")
	}()

	parser := newParser(inv.Agent, emit, onSession)
	reader := bufio.NewReaderSize(stdout, 256<<10)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			_, _ = raw.Write(line)
			parser.handle(line)
		}
		if readErr != nil {
			break
		}
	}
	waitErr := cmd.Wait()
	killProcessGroup(cmd)
	stderrTail := <-stderrDone

	outcome := parser.outcome()
	if outcome.DurationMS == 0 {
		outcome.DurationMS = time.Since(started).Milliseconds()
	}
	if inv.Agent == "codex" && inv.LastMessage != "" {
		if data, err := os.ReadFile(inv.LastMessage); err == nil && strings.TrimSpace(string(data)) != "" {
			outcome.Result = strings.TrimSpace(string(data))
		}
	}

	switch {
	case errors.Is(runCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil:
		return outcome, fmt.Errorf("运行超时（%s）", inv.Timeout)
	case ctx.Err() != nil:
		return outcome, context.Canceled
	case waitErr != nil:
		message := outcome.ErrorText
		if message == "" {
			message = stderrTail
		}
		if message == "" {
			message = waitErr.Error()
		}
		return outcome, fmt.Errorf("%s 退出异常: %s", inv.Agent, truncate(message, 2000))
	case outcome.IsError:
		message := outcome.ErrorText
		if message == "" {
			message = "Agent 报告执行失败"
		}
		return outcome, errors.New(truncate(message, 2000))
	}
	return outcome, nil
}

type parser struct {
	agent     string
	emit      func(Event)
	onSession func(string)
	result    Outcome
	lastText  string
}

func newParser(agent string, emit func(Event), onSession func(string)) *parser {
	return &parser{agent: agent, emit: emit, onSession: onSession}
}

func (p *parser) outcome() Outcome {
	outcome := p.result
	if outcome.Result == "" {
		outcome.Result = p.lastText
	}
	return outcome
}

func (p *parser) setSession(id string) {
	if id != "" && id != p.result.SessionID {
		p.result.SessionID = id
		if p.onSession != nil {
			p.onSession(id)
		}
	}
}

func (p *parser) handle(line []byte) {
	trimmed := strings.TrimSpace(string(line))
	if trimmed == "" {
		return
	}
	if !strings.HasPrefix(trimmed, "{") {
		p.emit(Event{Type: "stderr", Text: truncate(trimmed, 2000)})
		return
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
		return
	}
	if p.agent == "claude" {
		p.handleClaude(envelope)
	} else {
		p.handleCodex(envelope)
	}
}

func rawString(envelope map[string]json.RawMessage, key string) string {
	var value string
	if raw, ok := envelope[key]; ok {
		_ = json.Unmarshal(raw, &value)
	}
	return value
}

type claudeContent struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	ToolUseID string          `json:"tool_use_id"`
}

func (p *parser) handleClaude(envelope map[string]json.RawMessage) {
	switch rawString(envelope, "type") {
	case "system":
		if rawString(envelope, "subtype") == "init" {
			p.setSession(rawString(envelope, "session_id"))
			model := rawString(envelope, "model")
			text := "Claude Code 会话已启动"
			if model != "" {
				text += "（" + model + "）"
			}
			p.emit(Event{Type: "status", Text: text})
		}
	case "assistant", "user":
		var message struct {
			Content []claudeContent `json:"content"`
		}
		_ = json.Unmarshal(envelope["message"], &message)
		for _, content := range message.Content {
			switch content.Type {
			case "text":
				if text := strings.TrimSpace(content.Text); text != "" {
					p.lastText = text
					p.emit(Event{Type: "assistant", Text: text})
				}
			case "thinking":
				if text := strings.TrimSpace(content.Thinking); text != "" {
					p.emit(Event{Type: "thinking", Text: truncate(text, 4000)})
				}
			case "tool_use":
				p.emit(Event{Type: "tool", Tool: content.Name, Text: summarizeToolInput(content.Name, content.Input)})
			case "tool_result":
				text := toolResultText(content.Content)
				eventType := "tool_result"
				if content.IsError {
					eventType = "tool_error"
				}
				p.emit(Event{Type: eventType, Text: truncate(text, 1500)})
			}
		}
	case "result":
		var result struct {
			Result     string   `json:"result"`
			SessionID  string   `json:"session_id"`
			CostUSD    float64  `json:"total_cost_usd"`
			DurationMS int64    `json:"duration_ms"`
			IsError    bool     `json:"is_error"`
			Subtype    string   `json:"subtype"`
			Errors     []string `json:"errors"`
		}
		raw, _ := json.Marshal(envelope)
		_ = json.Unmarshal(raw, &result)
		p.setSession(result.SessionID)
		p.result.Result = strings.TrimSpace(result.Result)
		p.result.CostUSD = result.CostUSD
		p.result.DurationMS = result.DurationMS
		p.result.IsError = result.IsError
		if result.IsError {
			p.result.ErrorText = strings.TrimSpace(result.Result)
			if p.result.ErrorText == "" {
				p.result.ErrorText = strings.Join(result.Errors, "; ")
			}
			if p.result.ErrorText == "" {
				p.result.ErrorText = result.Subtype
			}
		}
		summary := fmt.Sprintf("完成，用时 %.1fs", float64(result.DurationMS)/1000)
		if result.CostUSD > 0 {
			summary += fmt.Sprintf("，费用约 $%.4f", result.CostUSD)
		}
		p.emit(Event{Type: "status", Text: summary})
	}
}

func (p *parser) handleCodex(envelope map[string]json.RawMessage) {
	switch rawString(envelope, "type") {
	case "thread.started":
		p.setSession(rawString(envelope, "thread_id"))
		p.emit(Event{Type: "status", Text: "Codex 会话已启动"})
	case "turn.completed":
		var usage struct {
			Usage struct {
				InputTokens  int64 `json:"input_tokens"`
				CachedTokens int64 `json:"cached_input_tokens"`
				OutputTokens int64 `json:"output_tokens"`
			} `json:"usage"`
		}
		raw, _ := json.Marshal(envelope)
		_ = json.Unmarshal(raw, &usage)
		p.emit(Event{Type: "status", Text: fmt.Sprintf("完成，输入 %d tokens（缓存 %d），输出 %d tokens",
			usage.Usage.InputTokens, usage.Usage.CachedTokens, usage.Usage.OutputTokens)})
	case "turn.failed":
		var failed struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		raw, _ := json.Marshal(envelope)
		_ = json.Unmarshal(raw, &failed)
		p.result.IsError = true
		p.result.ErrorText = failed.Error.Message
		p.emit(Event{Type: "error", Text: failed.Error.Message})
	case "error":
		message := rawString(envelope, "message")
		p.emit(Event{Type: "error", Text: message})
		if p.result.ErrorText == "" {
			p.result.ErrorText = message
		}
	case "item.started", "item.completed", "item.updated":
		eventType := rawString(envelope, "type")
		var item struct {
			Type             string `json:"type"`
			Text             string `json:"text"`
			Command          string `json:"command"`
			AggregatedOutput string `json:"aggregated_output"`
			ExitCode         *int   `json:"exit_code"`
			Status           string `json:"status"`
			Message          string `json:"message"`
			Server           string `json:"server"`
			Tool             string `json:"tool"`
			Query            string `json:"query"`
			Changes          []struct {
				Path string `json:"path"`
				Kind string `json:"kind"`
			} `json:"changes"`
			Items []struct {
				Text      string `json:"text"`
				Completed bool   `json:"completed"`
			} `json:"items"`
		}
		_ = json.Unmarshal(envelope["item"], &item)
		switch item.Type {
		case "agent_message":
			if eventType == "item.completed" && strings.TrimSpace(item.Text) != "" {
				p.lastText = strings.TrimSpace(item.Text)
				p.emit(Event{Type: "assistant", Text: p.lastText})
			}
		case "reasoning":
			if eventType == "item.completed" && strings.TrimSpace(item.Text) != "" {
				p.emit(Event{Type: "thinking", Text: truncate(strings.TrimSpace(item.Text), 4000)})
			}
		case "command_execution":
			if eventType == "item.started" {
				p.emit(Event{Type: "tool", Tool: "shell", Text: truncate(item.Command, 600)})
			} else if eventType == "item.completed" {
				text := strings.TrimSpace(item.AggregatedOutput)
				eventKind := "tool_result"
				if item.ExitCode != nil && *item.ExitCode != 0 {
					eventKind = "tool_error"
					text = fmt.Sprintf("退出码 %d\n%s", *item.ExitCode, text)
				}
				p.emit(Event{Type: eventKind, Text: truncate(text, 1500)})
			}
		case "file_change":
			if eventType == "item.completed" {
				parts := make([]string, 0, len(item.Changes))
				for _, change := range item.Changes {
					parts = append(parts, change.Kind+" "+change.Path)
				}
				p.emit(Event{Type: "tool", Tool: "edit", Text: strings.Join(parts, "\n")})
			}
		case "mcp_tool_call":
			if eventType == "item.started" {
				p.emit(Event{Type: "tool", Tool: "mcp", Text: item.Server + "." + item.Tool})
			}
		case "web_search":
			if eventType == "item.started" {
				p.emit(Event{Type: "tool", Tool: "web_search", Text: item.Query})
			}
		case "todo_list":
			if eventType != "item.started" {
				lines := make([]string, 0, len(item.Items))
				for _, todo := range item.Items {
					mark := "☐"
					if todo.Completed {
						mark = "☑"
					}
					lines = append(lines, mark+" "+todo.Text)
				}
				p.emit(Event{Type: "status", Text: strings.Join(lines, "\n")})
			}
		case "error":
			p.emit(Event{Type: "error", Text: item.Message})
		}
	}
}

func summarizeToolInput(name string, input json.RawMessage) string {
	var fields map[string]any
	if err := json.Unmarshal(input, &fields); err != nil {
		return ""
	}
	pick := func(keys ...string) string {
		for _, key := range keys {
			if value, ok := fields[key].(string); ok && value != "" {
				return value
			}
		}
		return ""
	}
	switch name {
	case "Bash":
		return truncate(pick("command"), 600)
	case "Read", "Edit", "MultiEdit", "Write", "NotebookEdit":
		return pick("file_path", "notebook_path")
	case "Grep":
		text := pick("pattern")
		if path := pick("path", "glob"); path != "" {
			text += "  in " + path
		}
		return text
	case "Glob":
		return pick("pattern")
	case "Skill":
		return pick("skill", "command", "name")
	case "Task":
		return pick("description", "prompt")
	case "WebFetch":
		return pick("url")
	case "WebSearch":
		return pick("query")
	}
	data, _ := json.Marshal(fields)
	return truncate(string(data), 400)
}

func toolResultText(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		values := make([]string, 0, len(parts))
		for _, part := range parts {
			if part.Text != "" {
				values = append(values, part.Text)
			}
		}
		return strings.TrimSpace(strings.Join(values, "\n"))
	}
	return ""
}

func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && (text[cut]&0xC0) == 0x80 {
		cut--
	}
	return text[:cut] + "…（已截断）"
}

// ResumeCommand 返回用户可在终端中继续该会话的命令。
func ResumeCommand(agent, dir, sessionID string) string {
	if sessionID == "" || dir == "" {
		return ""
	}
	quoted := shellQuote(dir)
	if agent == "codex" {
		return "cd " + quoted + " && codex resume " + sessionID
	}
	return "cd " + quoted + " && claude --resume " + sessionID
}

func shellQuote(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool {
		return !(r == '/' || r == '.' || r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
	}) < 0 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
