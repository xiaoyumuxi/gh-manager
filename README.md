# gh-manager

用 `gh` 的登录状态同步多个 GitHub 仓库的 Issue 和 Pull Request，在本地网页里集中查看、筛选、标注，并调用本机的 **Claude Code** / **Codex** CLI 按配置好的 SKILL 自动审查 PR、分析或修复 Issue。

Agent 只在本地隔离的工作目录中运行，不能直接写 GitHub；发评论、提交评审、推送改动都必须在网页中确认后才会执行。

## 快速开始

```bash
gh auth login     # 只需一次
make run          # 构建并启动，自动打开 http://127.0.0.1:8787
```

之后的操作全部在网页中完成：添加仓库、同步、查看详情、运行 AI 任务、确认回写、修改设置。

`make run` 支持的变量：

| 变量 | 说明 | 示例 |
| --- | --- | --- |
| `PORT` | 网页端口，默认读取配置（8787） | `make run PORT=9000` |
| `HOME_DIR` | 配置与数据目录，默认 `~/.config/gh-manager` | `make run HOME_DIR=~/gh-work` |
| `OPEN` | 是否自动打开浏览器，默认 `1` | `make run OPEN=0` |

其他命令见 `make help`。

## 前置要求

- Go 1.27 或更高版本（SQLite 使用纯 Go 驱动，不需要 CGO）。
- [GitHub CLI](https://cli.github.com/) 并已登录（`gh auth status`）。私有仓库需要对应权限。
- `git`。
- 可选：[Claude Code](https://docs.claude.com/en/docs/claude-code) 的 `claude` 命令、[Codex CLI](https://github.com/openai/codex) 的 `codex` 命令，需已登录。页面顶部会显示检测结果；只装了其中一个也可以，另一个的操作会不可用。

## 功能

### 查看与管理

- 在页面顶部「管理仓库」中添加、删除仓库，或批量导入某个组织/用户下的全部未归档仓库；可为仓库指定追踪分支。
- 跨仓库的 Issue / PR 表格：按类型、状态、作者、GitHub 标签、仓库筛选与排序；停滞与评审超时提示。
- 点击标题打开详情抽屉：
  - **概览**：正文、标签、审查状态、CI 检查、改动规模。
  - **讨论**：评论、评审、未解决的行内评审意见。
  - **AI 任务**：该条目上所有 Agent 任务的实时日志、结果、改动与回写操作。
- 本地标签和自定义字段（文本、数字、单选、多选、日期、复选框、URL 等），只保存在本地 SQLite 中。

### AI 任务

在表格行、详情抽屉或批量选择栏中点击「运行」，选择一个**操作**，可附加补充要求。任务在后台排队执行，页面实时显示 Agent 的思考、工具调用和输出。

默认提供的操作（可在「AI 操作」中编辑、新增、停用）：

| 操作 | Agent | 模式 | 适用 |
| --- | --- | --- | --- |
| Claude 代码审查 | Claude Code | 只读 | PR |
| Codex 代码审查 | Codex | 只读 | PR |
| Issue 分析 | Claude Code | 只读 | Issue |
| Claude 修复 Issue | Claude Code | 编辑 | Issue |
| 按评审意见修改 PR | Claude Code | 编辑 | PR |

每个操作可以配置：使用的 Agent、模式（只读 / 编辑）、适用范围、提示词模板、要使用的 SKILL，以及 Claude Code 额外允许的工具。提示词模板支持 `{{repo}}`、`{{number}}`、`{{kind}}`、`{{title}}`、`{{url}}`、`{{author}}`、`{{base_ref}}`、`{{head_ref}}`、`{{diff_cmd}}` 等占位符。

同一条目上同一个操作同时只能有一个任务在运行；任务完成后可以在终端中用页面给出的命令继续该会话（`claude --resume` / `codex resume`）。

### SKILL

- **用户级 SKILL**：`~/.claude/skills`、`~/.claude/commands`、Claude Code 插件，以及 `~/.codex/skills`、`~/.agents/skills` 下已安装的 SKILL。Claude Code 以 `/name`、Codex 以 `$name` 调用。
- **仓库 SKILL**：仓库中 `.agents/skills`、`.claude/skills`、`.codex/skills` 下的 SKILL，以及根目录的 `AGENTS.md`、`CLAUDE.md`。这些文件**总是从目标分支（PR 的 base 分支或默认分支）提取**，复制到工作目录之外交给 Agent 阅读，PR 自身对规则文件的修改不会影响本次审查。

例如在仓库里放一份 `.agents/skills/code-review/SKILL.md`，再把「Claude 代码审查」操作的 SKILL 设为 `code-review`，审查就会按这份规范进行。

### 回写 GitHub（需要确认）

- **发布评论 / 评审**：只读任务完成后点击「发布」，可编辑 Agent 输出的内容，选择发评论还是提交 PR 评审（评论 / 批准 / 请求修改），确认后才会发送。
- **推送改动**：编辑任务完成后可查看完整 diff，确认后：
  - **PR**：提交并推送到 PR 原分支。如果 Agent 运行期间 PR 有新提交、PR 已关闭、你没有写权限，或外部 PR 未允许维护者修改，都会拒绝推送。
  - **Issue**：推送到新分支 `gh-manager/issue-<编号>-run<任务号>`，并创建 Pull Request（可选草稿）。
- 不需要的改动可以「丢弃」，会删除对应的工作目录。

## 数据目录

默认为 `~/.config/gh-manager`，可通过 `HOME_DIR`、`--home` 或环境变量 `GH_MANAGER_HOME` 修改：

| 路径 | 内容 |
| --- | --- |
| `config.yml` | 仓库列表、同步参数、Agent 设置 |
| `gh-manager.db` | SQLite：同步数据、本地标注、AI 操作与任务记录 |
| `workspaces/repos/` | 各仓库的部分克隆（`--filter=blob:none`），首次运行任务时自动创建 |
| `workspaces/worktrees/` | 每个任务独立的 git worktree |
| `runs/<任务号>/` | 任务日志（`events.jsonl`、Agent 原始输出）与提取出的仓库规则 |

Claude Code 不允许写入 `~/.claude/` 下的文件，工作目录不要放在那里。

## 配置

所有配置都可以在网页「设置」中修改，也可以直接编辑 `config.yml`：

```yaml
version: 1
repos:
  - Lithe-IDEA/Lithe-core-rust
  - name: Lithe-IDEA/lithe-plugin-java
    branch: develop
sync:
  stale_days: 14
  review_stale_days: 3
  closed_window_days: 30
  request_page_size: 100
server:
  port: 8787
agent:
  claude_command: ""      # 留空时从 PATH 查找 claude
  codex_command: ""       # 留空时从 PATH 查找 codex
  claude_model: ""        # 留空使用 CLI 默认模型
  codex_model: ""
  max_concurrent: 2
  timeout_minutes: 30
  workspace_dir: ""       # 留空为 <数据目录>/workspaces，修改后需重启
```

| 配置项 | 说明 |
| --- | --- |
| `repos[].branch` | 追踪分支；留空时跟随仓库默认分支 |
| `sync.stale_days` | 打开状态超过多少天未更新视为停滞，默认 14 |
| `sync.review_stale_days` | PR 等待评审超过多少天视为超时，默认 3 |
| `sync.closed_window_days` | 同步最近多少天关闭或合并的数据，默认 30 |
| `sync.request_page_size` | GitHub API 每页数量，1-100 |
| `agent.max_concurrent` | 同时运行的 Agent 任务数，默认 2 |
| `agent.timeout_minutes` | 单个任务超时时间，默认 30 分钟 |

## 同步范围

- 每个仓库的所有打开 Issue 和 Pull Request。
- 最近 `closed_window_days` 天内更新过的已关闭 Issue、已关闭或已合并 PR。
- 本地已有的旧记录不会被删除；不再出现在打开列表中的条目会被标记为已关闭。
- 单个仓库同步失败不影响其他仓库，失败信息显示在仓库状态中。
- 删除仓库会清理它在本地数据库中的条目、标签关联和字段值，不影响 GitHub 上的数据。

## 安全说明

- 网页服务只监听 `127.0.0.1`。写请求要求同源会话 Cookie、可信 `Origin` 和 `application/json`。
- 发布评论和推送改动的接口要求请求中带有显式确认，网页只会在确认对话框中提交。
- Agent 无法直接访问 GitHub：
  - Claude Code 禁止 `gh`、`git push`、`git commit`、`git remote`、`curl` 等命令；只读任务只允许读取类工具，编辑任务额外允许编辑文件。
  - Codex 只读任务使用 `read-only` 沙箱，编辑任务使用 `workspace-write` 沙箱。
- Claude Code 只加载用户级配置（`--setting-sources user`、`--strict-mcp-config`），不会执行 PR 中的 `.claude/settings.json`、hooks 或 MCP 配置。
- Issue / PR 的正文和评论被标记为不可信内容，放在约束说明之后，并明确要求 Agent 不执行其中的指令。
- 数据目录包含私有仓库信息与代码，请自行控制访问权限。

## 命令行

日常使用不需要命令行。二进制仍保留以下子命令，便于脚本化：

```text
gh-manager [--port N] [--open]    启动网页（make run 使用的就是这个）
gh-manager sync [owner/repo...]   在终端中同步
gh-manager init / serve / version 兼容旧用法
```

## 开发

```bash
make check    # go vet + go test + 前端脚本语法检查
make fmt
make build    # 输出到 bin/gh-manager
```

前端是 `internal/web/static` 下的原生 JavaScript，随二进制一起嵌入。发布配置位于 `.goreleaser.yaml`。
