# gh-manager

用 `gh` 的登录状态同步多个 GitHub 仓库的 Issue 和 Pull Request，在本地网页里以类似多维表格的方式集中查看、筛选和标注。

所有标签和自定义字段都保存在本地 SQLite 中，不会修改 GitHub 上的 Issue 或 PR。

## 功能

- 跨仓库同步 Issue 与 Pull Request，支持组织、用户名或显式仓库列表。
- 表格内按仓库、类型、状态、作者、GitHub 标签等筛选与排序。
- 本地标签：默认提供“重点、待处理、进行中、待跟进”，可自行增删改。
- 自定义字段：文本、长文本、数字、单选、多选、日期、复选框、URL。
- 停滞与评审超时提示，按最后更新时间计算。
- 同步可手动触发，页面轮询展示同步状态；重复同步不会覆盖本地标注。

## 前置要求

- Go 1.27 或更高版本。
- 已安装 [GitHub CLI](https://cli.github.com/) 并完成登录：

```bash
gh auth login
gh auth status
```

`gh-manager` 通过 `gh` 的登录凭据访问 GitHub GraphQL API。私有仓库需要对应权限。

## 安装与构建

从源码构建：

```bash
git clone https://github.com/xiaoyumuxi/gh-manager.git
cd gh-manager
go build -o gh-manager .
```

也可以直接安装到 `$GOBIN`：

```bash
go install github.com/xiaoyumuxi/gh-manager@latest
```

SQLite 使用纯 Go 驱动，构建时不需要 CGO。

## 快速开始

1. 初始化并指定要跟踪的仓库：

```bash
./gh-manager init --repos Lithe-IDEA/Lithe-core-rust,Lithe-IDEA/lithe-plugin-java
```

也可以读取组织或用户名下的全部未归档仓库：

```bash
./gh-manager init --org Lithe-IDEA
```

2. 同步数据：

```bash
./gh-manager sync
```

3. 启动本地网页：

```bash
./gh-manager serve
```

打开 <http://127.0.0.1:8787>。

## 命令

```text
gh-manager init [owner/repo...]   创建配置并选择仓库
gh-manager sync [owner/repo...]   同步 GitHub 数据；不传仓库时使用配置
gh-manager serve                  启动本地网页
gh-manager version                查看版本
```

常用参数：

- `--home <目录>`：指定配置与数据库目录，也可使用环境变量 `GH_MANAGER_HOME`。
- `init --repos a/b,c/d`：直接指定仓库，支持逗号、空格或重复传参。
- `init --org <owner>`：拉取该组织或用户下的未归档仓库。
- `init --force`：覆盖已有配置。
- `init --port 8787`：设置网页端口。
- `serve --port 8787`：临时覆盖网页端口。

默认数据目录为 `~/.config/gh-manager`，其中：

- `config.yml`：仓库列表与同步参数。
- `gh-manager.db`：本地 SQLite 数据库，包含同步数据、标签和字段值。

使用独立目录隔离多套数据：

```bash
GH_MANAGER_HOME=~/gh-manager-work ./gh-manager init --repos owner/repo
GH_MANAGER_HOME=~/gh-manager-work ./gh-manager sync
GH_MANAGER_HOME=~/gh-manager-work ./gh-manager serve
```

## 配置

`config.yml` 示例：

```yaml
version: 1
repos:
  - Lithe-IDEA/Lithe-core-rust
  - Lithe-IDEA/lithe-plugin-java
sync:
  stale_days: 14
  review_stale_days: 3
  closed_window_days: 30
  request_page_size: 100
server:
  port: 8787
```

| 配置项 | 说明 |
| --- | --- |
| `repos` | 要同步的仓库，格式为 `owner/name` |
| `sync.stale_days` | 打开状态超过多少天未更新视为停滞，默认 14 |
| `sync.review_stale_days` | PR 等待评审超过多少天视为超时，默认 3 |
| `sync.closed_window_days` | 同步最近多少天关闭或合并的数据，默认 30 |
| `sync.request_page_size` | GitHub API 每页数量，范围 1-100 |
| `server.port` | 本地网页端口，默认 8787 |

## 同步范围

- 每个仓库的所有打开 Issue 和 Pull Request。
- 最近 `closed_window_days` 天内更新过的已关闭 Issue。
- 最近 `closed_window_days` 天内更新的已关闭或已合并 Pull Request。
- 已存在于本地数据库的旧记录不会被删除；如果某个打开条目不再出现在完整的打开列表中，会被标记为已关闭。

单个仓库同步失败不会影响其他仓库，失败信息会写入同步记录和仓库状态。

## 本地标注

标签和自定义字段只写入 `gh-manager.db`：

- 标签可以新建、改名、改色、删除，并赋予任意条目。
- 删除标签或字段时，对应的条目关联值会一并清理。
- 自定义字段可随时新增，已有条目默认没有值。
- GitHub 同步只更新 GitHub 来源字段，不会清空本地标签和字段值。

当前版本不包含：手动新增非 GitHub 行、回写 GitHub 状态或评论、内置工作量统计。

## 安全说明

- 网页服务只监听 `127.0.0.1`，不会暴露到局域网或公网。
- 写请求要求同源会话 Cookie、可信 `Origin` 和 `application/json`。
- 数据库包含私有仓库信息，请自行控制 `~/.config/gh-manager` 的访问权限。

## 开发

```bash
gofmt -w .
go test ./...
go build -o gh-manager .
node --check internal/web/static/app.js
```

发布配置位于 `.goreleaser.yaml`，构建时设置 `CGO_ENABLED=0`，支持 macOS、Linux 和 Windows 的 amd64/arm64。
