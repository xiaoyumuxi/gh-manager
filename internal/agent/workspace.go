package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/xiaoyumuxi/gh-manager/internal/config"
)

// Workspace 管理每个仓库的本地裸克隆以及每次运行的 git worktree。
type Workspace struct {
	root      string
	ghPath    string
	gitPath   string
	mu        sync.Mutex
	repoLocks map[string]chan struct{}
}

// Prepared 是一次运行可用的工作目录信息。
type Prepared struct {
	Dir      string
	StartSHA string
	BaseRef  string
	// BaseName 是 worktree 中可直接使用的目标分支名，例如 origin/main。
	BaseName string
	BaseTip  string
	HeadRef  string
	HeadRepo string
}

// PrepareRequest 描述需要检出的代码。
type PrepareRequest struct {
	RunID      int64
	Repo       string
	Kind       string
	Number     int
	BaseBranch string
	HeadRef    string
	HeadRepo   string
}

var safeRefPart = regexp.MustCompile(`[^A-Za-z0-9._\-/]`)

func NewWorkspace(root string) (*Workspace, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("创建工作区目录失败: %w", err)
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, errors.New("未找到 git，请先安装 git")
	}
	ghPath, _ := exec.LookPath("gh")
	return &Workspace{root: root, ghPath: ghPath, gitPath: gitPath, repoLocks: map[string]chan struct{}{}}, nil
}

func (w *Workspace) Root() string { return w.root }

func (w *Workspace) repoLock(repo string) chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	key := strings.ToLower(repo)
	lock, ok := w.repoLocks[key]
	if !ok {
		lock = make(chan struct{}, 1)
		w.repoLocks[key] = lock
	}
	return lock
}

// lockRepo 串行化同一仓库的 git 操作；等待期间可被取消，onWait 在需要排队时调用一次。
func (w *Workspace) lockRepo(ctx context.Context, repo string, onWait func()) (func(), error) {
	lock := w.repoLock(repo)
	select {
	case lock <- struct{}{}:
	default:
		if onWait != nil {
			onWait()
		}
		select {
		case lock <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return func() { <-lock }, nil
}

func (w *Workspace) repoDir(repo string) string {
	owner, name, _ := config.SplitRepo(repo)
	return filepath.Join(w.root, "repos", strings.ToLower(owner), strings.ToLower(name)+".git")
}

func (w *Workspace) credentialArgs() []string {
	if w.ghPath == "" {
		return nil
	}
	return []string{"-c", "credential.helper=", "-c", fmt.Sprintf("credential.helper=!'%s' auth git-credential", w.ghPath)}
}

func (w *Workspace) baseArgs() []string {
	args := []string{"-c", "core.hooksPath=" + os.DevNull, "-c", "protocol.file.allow=never"}
	return append(args, w.credentialArgs()...)
}

func (w *Workspace) env() []string {
	return append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "LC_ALL=C")
}

// git 在指定目录执行 git 命令，返回去掉首尾空白的标准输出。
func (w *Workspace) git(ctx context.Context, dir string, args ...string) (string, error) {
	output, err := w.gitBytes(ctx, dir, nil, args...)
	return strings.TrimSpace(string(output)), err
}

func (w *Workspace) gitBytes(ctx context.Context, dir string, stdin io.Reader, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, w.gitPath, append(w.baseArgs(), args...)...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = w.env()
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		name := ""
		for _, arg := range args {
			if !strings.HasPrefix(arg, "-") && !strings.Contains(arg, "=") {
				name = arg
				break
			}
		}
		return nil, fmt.Errorf("git %s: %s", name, lastLines(message, 6))
	}
	return stdout.Bytes(), nil
}

func validateBranch(branch string) error {
	if branch == "" {
		return errors.New("分支名为空")
	}
	return config.ValidateBranchName(branch)
}

func (w *Workspace) ensureClone(ctx context.Context, repo string, progress func(string)) (string, error) {
	dir := w.repoDir(repo)
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err == nil {
		return dir, nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	progress(fmt.Sprintf("首次使用，正在克隆 %s（仅元数据，按需下载文件）…", repo))
	tmp := dir + ".partial"
	_ = os.RemoveAll(tmp)
	url := "https://github.com/" + repo + ".git"
	if _, err := w.git(ctx, "", "clone", "--bare", "--filter=blob:none", "--no-tags", url, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return "", fmt.Errorf("克隆仓库失败: %w", err)
	}
	if err := os.Rename(tmp, dir); err != nil {
		return "", err
	}
	// 写入本地凭据配置，Agent 在 worktree 中执行 git diff 等命令按需下载文件时也能访问私有仓库。
	if w.ghPath != "" {
		_, _ = w.git(ctx, dir, "config", "--local", "--replace-all", "credential.helper", "")
		_, _ = w.git(ctx, dir, "config", "--local", "--add", "credential.helper", fmt.Sprintf("!'%s' auth git-credential", w.ghPath))
	}
	_, _ = w.git(ctx, dir, "config", "--local", "gc.auto", "0")
	return dir, nil
}

// Prepare 拉取最新代码并为本次运行创建独立 worktree。
func (w *Workspace) Prepare(ctx context.Context, request PrepareRequest, progress func(string)) (Prepared, error) {
	if progress == nil {
		progress = func(string) {}
	}
	unlock, err := w.lockRepo(ctx, request.Repo, func() { progress("同一仓库的其他任务正在准备代码，排队等待…") })
	if err != nil {
		return Prepared{}, err
	}
	defer unlock()

	repoDir, err := w.ensureClone(ctx, request.Repo, progress)
	if err != nil {
		return Prepared{}, err
	}
	prepared := Prepared{BaseRef: request.BaseBranch, HeadRef: request.HeadRef, HeadRepo: request.HeadRepo}
	if prepared.BaseRef == "" {
		branch, err := w.defaultBranch(ctx, repoDir)
		if err != nil {
			return Prepared{}, err
		}
		prepared.BaseRef = branch
	}
	if err := validateBranch(prepared.BaseRef); err != nil {
		return Prepared{}, fmt.Errorf("基础分支名无效: %w", err)
	}
	// 目标分支放在 refs/remotes/origin/ 下，Agent 可以直接使用 origin/<base>，与本地开发习惯一致。
	baseLocal := "refs/remotes/origin/" + prepared.BaseRef
	prepared.BaseName = "origin/" + prepared.BaseRef
	refspecs := []string{"+refs/heads/" + prepared.BaseRef + ":" + baseLocal}
	checkout := baseLocal
	if request.Kind == "pr" {
		prLocal := fmt.Sprintf("refs/gh-manager/pr/%d", request.Number)
		refspecs = append(refspecs, fmt.Sprintf("+refs/pull/%d/head:%s", request.Number, prLocal))
		checkout = prLocal
	}
	progress("正在拉取最新代码…")
	args := append([]string{"fetch", "--no-tags", "--filter=blob:none", "origin"}, refspecs...)
	if _, err := w.git(ctx, repoDir, args...); err != nil {
		return Prepared{}, fmt.Errorf("拉取代码失败: %w", err)
	}
	if prepared.BaseTip, err = w.git(ctx, repoDir, "rev-parse", baseLocal); err != nil {
		return Prepared{}, err
	}
	startSHA, err := w.git(ctx, repoDir, "rev-parse", checkout+"^{commit}")
	if err != nil {
		return Prepared{}, err
	}

	owner, name, _ := config.SplitRepo(request.Repo)
	label := fmt.Sprintf("%s-%s-%s%d-run%d", owner, name, request.Kind, request.Number, request.RunID)
	dir := filepath.Join(w.root, "worktrees", safeRefPart.ReplaceAllString(strings.ToLower(label), "_"))
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return Prepared{}, err
	}
	_ = os.RemoveAll(dir)
	_, _ = w.git(ctx, repoDir, "worktree", "prune")
	progress("正在检出代码…")
	if _, err := w.git(ctx, repoDir, "worktree", "add", "--detach", "--force", dir, startSHA); err != nil {
		return Prepared{}, fmt.Errorf("创建工作目录失败: %w", err)
	}
	if request.Kind == "pr" {
		// 部分克隆按需下载文件；先把 diff 涉及的两侧文件下载好，
		// 这样在禁止联网的沙箱（例如 Codex）中也能正常执行 git diff。
		progress("正在下载改动涉及的文件…")
		_, _ = w.git(ctx, repoDir, "diff", "--numstat", prepared.BaseTip+"..."+startSHA)
	}
	prepared.Dir = dir
	prepared.StartSHA = startSHA
	return prepared, nil
}

func (w *Workspace) defaultBranch(ctx context.Context, repoDir string) (string, error) {
	output, err := w.git(ctx, repoDir, "ls-remote", "--symref", "origin", "HEAD")
	if err != nil {
		return "", fmt.Errorf("读取默认分支失败: %w", err)
	}
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "ref: refs/heads/") {
			fields := strings.Fields(strings.TrimPrefix(line, "ref: refs/heads/"))
			if len(fields) > 0 {
				return fields[0], nil
			}
		}
	}
	return "", errors.New("无法确定仓库默认分支")
}

// Diff 返回工作目录相对起始提交的改动（包含未跟踪文件）。
func (w *Workspace) Diff(ctx context.Context, dir, startSHA string, maxBytes int) (stat string, patch string, truncated bool, err error) {
	if _, err := os.Stat(dir); err != nil {
		return "", "", false, errors.New("工作目录已被清理")
	}
	if _, err := w.git(ctx, dir, "add", "-A"); err != nil {
		return "", "", false, err
	}
	stat, err = w.git(ctx, dir, "diff", "--cached", "--stat=120", startSHA)
	if err != nil {
		return "", "", false, err
	}
	patch, err = w.git(ctx, dir, "diff", "--cached", "--no-color", "--no-ext-diff", startSHA)
	if err != nil {
		return "", "", false, err
	}
	if maxBytes > 0 && len(patch) > maxBytes {
		patch = patch[:maxBytes]
		truncated = true
	}
	return stat, patch, truncated, nil
}

// CommitAll 把工作目录相对起始提交的全部改动压成一个提交，返回新提交 SHA。
func (w *Workspace) CommitAll(ctx context.Context, dir, startSHA, message, fallbackName, fallbackEmail string) (string, error) {
	if _, err := os.Stat(dir); err != nil {
		return "", errors.New("工作目录已被清理，无法提交")
	}
	if _, err := w.git(ctx, dir, "add", "-A"); err != nil {
		return "", err
	}
	if _, err := w.git(ctx, dir, "reset", "--soft", startSHA); err != nil {
		return "", err
	}
	if _, err := w.git(ctx, dir, "diff", "--cached", "--quiet", startSHA); err == nil {
		return "", errors.New("没有可提交的改动")
	}
	args := []string{}
	if email, _ := w.git(ctx, dir, "config", "user.email"); email == "" && fallbackEmail != "" {
		args = append(args, "-c", "user.email="+fallbackEmail, "-c", "user.name="+fallbackName)
	}
	args = append(args, "commit", "--no-verify", "-q", "-F", "-")
	if _, err := w.gitWithInput(ctx, dir, message, args...); err != nil {
		return "", fmt.Errorf("提交失败: %w", err)
	}
	return w.git(ctx, dir, "rev-parse", "HEAD")
}

// Push 将 HEAD 推送到目标仓库分支（不使用 force，远端有新提交时会被拒绝）。
func (w *Workspace) Push(ctx context.Context, dir, targetRepo, branch string) error {
	if err := validateBranch(branch); err != nil {
		return fmt.Errorf("目标分支无效: %q", branch)
	}
	if _, _, err := config.SplitRepo(targetRepo); err != nil {
		return err
	}
	url := "https://github.com/" + targetRepo + ".git"
	if _, err := w.git(ctx, dir, "push", "--no-verify", url, "HEAD:refs/heads/"+branch); err != nil {
		if strings.Contains(err.Error(), "non-fast-forward") || strings.Contains(err.Error(), "fetch first") || strings.Contains(err.Error(), "rejected") {
			return fmt.Errorf("远端分支已有新的提交，已拒绝覆盖。请重新运行 Agent 后再提交: %w", err)
		}
		return fmt.Errorf("推送失败: %w", err)
	}
	return nil
}

// Remove 删除运行对应的 worktree。
func (w *Workspace) Remove(ctx context.Context, repo, dir string) error {
	if dir == "" {
		return nil
	}
	clean := filepath.Clean(dir)
	if !strings.HasPrefix(clean, filepath.Join(w.root, "worktrees")+string(filepath.Separator)) {
		return errors.New("拒绝删除工作区以外的目录")
	}
	unlock, err := w.lockRepo(ctx, repo, nil)
	if err != nil {
		return err
	}
	defer unlock()
	repoDir := w.repoDir(repo)
	if _, err := os.Stat(repoDir); err == nil {
		_, _ = w.git(ctx, repoDir, "worktree", "remove", "--force", clean)
		_, _ = w.git(ctx, repoDir, "worktree", "prune")
	}
	if err := os.RemoveAll(clean); err != nil {
		return err
	}
	return nil
}

func (w *Workspace) gitWithInput(ctx context.Context, dir, input string, args ...string) (string, error) {
	output, err := w.gitBytes(ctx, dir, strings.NewReader(input), args...)
	return strings.TrimSpace(string(output)), err
}

func lastLines(text string, count int) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	return strings.Join(lines, "\n")
}
