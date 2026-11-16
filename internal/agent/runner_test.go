package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func runParser(t *testing.T, agent string, lines ...string) ([]Event, Outcome, []string) {
	t.Helper()
	var events []Event
	var sessions []string
	parser := newParser(agent, func(event Event) { events = append(events, event) }, func(id string) { sessions = append(sessions, id) })
	for _, line := range lines {
		parser.handle([]byte(line))
	}
	return events, parser.outcome(), sessions
}

func eventTypes(events []Event) []string {
	types := make([]string, len(events))
	for i, event := range events {
		types[i] = event.Type
	}
	return types
}

func TestClaudeStreamParser(t *testing.T) {
	events, outcome, sessions := runParser(t, "claude",
		`{"type":"system","subtype":"init","session_id":"sess-1","model":"claude-opus-5-5"}`,
		`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"先看 diff"},{"type":"text","text":"开始审查"},{"type":"tool_use","name":"Bash","input":{"command":"git diff origin/main...HEAD"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","content":[{"type":"text","text":"diff --git a/x b/x"}]}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":"permission denied"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/repo/main.go"}}]}}`,
		`not json, probably stderr`,
		``,
		`{"type":"result","subtype":"success","result":"## 结论\n✅ 可以合并","session_id":"sess-1","total_cost_usd":0.0123,"duration_ms":4500,"is_error":false}`,
	)

	want := []string{"status", "thinking", "assistant", "tool", "tool_result", "tool_error", "tool", "stderr", "status"}
	if got := eventTypes(events); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("事件类型 = %v，期望 %v", got, want)
	}
	if !strings.Contains(events[0].Text, "claude-opus-5-5") {
		t.Errorf("init 事件应包含模型名：%q", events[0].Text)
	}
	if events[3].Tool != "Bash" || events[3].Text != "git diff origin/main...HEAD" {
		t.Errorf("Bash 工具摘要错误：%+v", events[3])
	}
	if events[4].Text != "diff --git a/x b/x" {
		t.Errorf("tool_result 文本错误：%q", events[4].Text)
	}
	if events[5].Text != "permission denied" {
		t.Errorf("tool_error 文本错误：%q", events[5].Text)
	}
	if events[6].Tool != "Read" || events[6].Text != "/repo/main.go" {
		t.Errorf("Read 工具摘要错误：%+v", events[6])
	}
	if !strings.Contains(events[8].Text, "4.5s") || !strings.Contains(events[8].Text, "$0.0123") {
		t.Errorf("完成摘要错误：%q", events[8].Text)
	}
	if len(sessions) != 1 || sessions[0] != "sess-1" {
		t.Errorf("会话回调 = %v，期望只回调一次 sess-1", sessions)
	}
	if outcome.SessionID != "sess-1" || outcome.Result != "## 结论\n✅ 可以合并" || outcome.IsError {
		t.Errorf("结果错误：%+v", outcome)
	}
	if outcome.CostUSD != 0.0123 || outcome.DurationMS != 4500 {
		t.Errorf("费用或耗时错误：%+v", outcome)
	}
}

func TestClaudeStreamParserError(t *testing.T) {
	_, outcome, _ := runParser(t, "claude",
		`{"type":"result","subtype":"error_max_turns","result":"","is_error":true,"errors":["达到最大轮数"]}`,
	)
	if !outcome.IsError || outcome.ErrorText != "达到最大轮数" {
		t.Fatalf("错误结果解析错误：%+v", outcome)
	}

	_, outcome, _ = runParser(t, "claude", `{"type":"result","subtype":"error_during_execution","is_error":true}`)
	if outcome.ErrorText != "error_during_execution" {
		t.Fatalf("没有错误文本时应回退到 subtype：%+v", outcome)
	}
}

func TestClaudeParserFallsBackToLastText(t *testing.T) {
	_, outcome, _ := runParser(t, "claude",
		`{"type":"assistant","message":{"content":[{"type":"text","text":"第一段"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"  最终结论  "}]}}`,
	)
	if outcome.Result != "最终结论" {
		t.Fatalf("没有 result 事件时应使用最后一段文本，得到 %q", outcome.Result)
	}
}

func TestCodexJSONParser(t *testing.T) {
	events, outcome, sessions := runParser(t, "codex",
		`{"type":"thread.started","thread_id":"thread-9"}`,
		`{"type":"turn.started"}`,
		`{"type":"item.completed","item":{"id":"i0","type":"reasoning","text":"读一下代码"}}`,
		`{"type":"item.started","item":{"id":"i1","type":"command_execution","command":"bash -lc 'git diff'","status":"in_progress"}}`,
		`{"type":"item.completed","item":{"id":"i1","type":"command_execution","command":"bash -lc 'git diff'","aggregated_output":"ok\n","exit_code":0,"status":"completed"}}`,
		`{"type":"item.completed","item":{"id":"i2","type":"command_execution","command":"false","aggregated_output":"boom","exit_code":2,"status":"failed"}}`,
		`{"type":"item.completed","item":{"id":"i3","type":"file_change","changes":[{"path":"a.go","kind":"update"},{"path":"b.go","kind":"add"}],"status":"completed"}}`,
		`{"type":"item.updated","item":{"id":"i4","type":"todo_list","items":[{"text":"读代码","completed":true},{"text":"写结论","completed":false}]}}`,
		`{"type":"item.started","item":{"id":"i5","type":"agent_message","text":""}}`,
		`{"type":"item.completed","item":{"id":"i5","type":"agent_message","text":"审查完成"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":1200,"cached_input_tokens":800,"output_tokens":300}}`,
	)

	want := []string{"status", "thinking", "tool", "tool_result", "tool_error", "tool", "status", "assistant", "status"}
	if got := eventTypes(events); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("事件类型 = %v，期望 %v", got, want)
	}
	if events[2].Tool != "shell" || events[2].Text != "bash -lc 'git diff'" {
		t.Errorf("命令事件错误：%+v", events[2])
	}
	if events[3].Text != "ok" {
		t.Errorf("命令输出错误：%q", events[3].Text)
	}
	if !strings.HasPrefix(events[4].Text, "退出码 2") {
		t.Errorf("失败命令应带退出码：%q", events[4].Text)
	}
	if events[5].Tool != "edit" || events[5].Text != "update a.go\nadd b.go" {
		t.Errorf("文件改动事件错误：%+v", events[5])
	}
	if events[6].Text != "☑ 读代码\n☐ 写结论" {
		t.Errorf("待办事件错误：%q", events[6].Text)
	}
	if !strings.Contains(events[8].Text, "1200") || !strings.Contains(events[8].Text, "800") || !strings.Contains(events[8].Text, "300") {
		t.Errorf("用量摘要错误：%q", events[8].Text)
	}
	if len(sessions) != 1 || sessions[0] != "thread-9" {
		t.Errorf("会话回调 = %v", sessions)
	}
	if outcome.SessionID != "thread-9" || outcome.Result != "审查完成" || outcome.IsError {
		t.Errorf("结果错误：%+v", outcome)
	}
}

func TestCodexJSONParserFailure(t *testing.T) {
	events, outcome, _ := runParser(t, "codex",
		`{"type":"error","message":"stream disconnected"}`,
		`{"type":"turn.failed","error":{"message":"rate limited"}}`,
	)
	if got := eventTypes(events); strings.Join(got, ",") != "error,error" {
		t.Fatalf("事件类型 = %v", got)
	}
	if !outcome.IsError || outcome.ErrorText != "rate limited" {
		t.Fatalf("turn.failed 应标记错误并以其消息为准：%+v", outcome)
	}
}

func TestTruncateKeepsUTF8Boundary(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Fatalf("未超长文本不应截断：%q", got)
	}
	text := strings.Repeat("中", 10) // 每个字 3 字节
	got := truncate(text, 4)
	if !utf8.ValidString(got) {
		t.Fatalf("截断结果不是合法 UTF-8：%q", got)
	}
	if !strings.HasPrefix(got, "中") || strings.HasPrefix(got, "中中") {
		t.Fatalf("截断位置错误：%q", got)
	}
	if !strings.HasSuffix(got, "（已截断）") {
		t.Fatalf("截断结果应带标记：%q", got)
	}
}

func TestResumeCommand(t *testing.T) {
	cases := []struct {
		agent, dir, session, want string
	}{
		{"claude", "/tmp/run-1", "abc", "cd /tmp/run-1 && claude --resume abc"},
		{"codex", "/tmp/run-1", "t-1", "cd /tmp/run-1 && codex resume t-1"},
		{"claude", "/Users/me/My Runs/it's", "abc", `cd '/Users/me/My Runs/it'\''s' && claude --resume abc`},
		{"claude", "", "abc", ""},
		{"claude", "/tmp", "", ""},
	}
	for _, tc := range cases {
		if got := ResumeCommand(tc.agent, tc.dir, tc.session); got != tc.want {
			t.Errorf("ResumeCommand(%q, %q, %q) = %q，期望 %q", tc.agent, tc.dir, tc.session, got, tc.want)
		}
	}
	if got := shellQuote("$(rm -rf ~)"); got != "'$(rm -rf ~)'" {
		t.Errorf("shellQuote 应加引号：%q", got)
	}
	if got := shellQuote(""); got != "''" {
		t.Errorf("空字符串应加引号：%q", got)
	}
}

func TestSplitTools(t *testing.T) {
	got := splitTools(" Read, Grep\nBash(git diff:*) ,, \n")
	want := []string{"Read", "Grep", "Bash(git diff:*)"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("splitTools = %v，期望 %v", got, want)
	}
	if got := splitTools(""); len(got) != 0 {
		t.Fatalf("空字符串应返回空列表：%v", got)
	}
}

func TestToolResultText(t *testing.T) {
	if got := toolResultText([]byte(`"  plain  "`)); got != "plain" {
		t.Errorf("字符串结果错误：%q", got)
	}
	if got := toolResultText([]byte(`[{"type":"text","text":"a"},{"type":"image"},{"type":"text","text":"b"}]`)); got != "a\nb" {
		t.Errorf("数组结果错误：%q", got)
	}
	if got := toolResultText([]byte(`{"weird":true}`)); got != "" {
		t.Errorf("未知结构应返回空：%q", got)
	}
}

func TestCanWrite(t *testing.T) {
	for _, permission := range []string{"ADMIN", "MAINTAIN", "WRITE"} {
		if !canWrite(permission) {
			t.Errorf("%s 应有写权限", permission)
		}
	}
	for _, permission := range []string{"", "READ", "TRIAGE", "write"} {
		if canWrite(permission) {
			t.Errorf("%q 不应有写权限", permission)
		}
	}
}
