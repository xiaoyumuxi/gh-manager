package agent

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// 仓库内的 Agent 规则：根目录说明文件与各工具约定的 SKILL 目录。
var rulePathspecs = []string{"AGENTS.md", "CLAUDE.md", ".agents/skills", ".claude/skills", ".codex/skills"}

var repoSkillFile = regexp.MustCompile(`^(\.agents|\.claude|\.codex)/skills/([^/]+)/SKILL\.md$`)

const (
	maxRuleFiles     = 400
	maxRuleFileBytes = 512 << 10
	maxRuleBytes     = 8 << 20
)

// RepoRules 是从目标分支提取出的仓库规则副本。
// 副本放在 worktree 之外，PR 对这些文件的修改不会影响 Agent 遵循的规则。
type RepoRules struct {
	Dir       string
	Commit    string
	Files     []string
	Skills    []Skill
	Truncated bool
}

// FindSkill 按名称查找仓库 SKILL。
func (r *RepoRules) FindSkill(name string) (Skill, bool) {
	if r == nil {
		return Skill{}, false
	}
	for _, skill := range r.Skills {
		if strings.EqualFold(skill.Name, name) || strings.EqualFold(path.Base(skill.Source), name) {
			return skill, true
		}
	}
	return Skill{}, false
}

type treeEntry struct {
	mode string
	kind string
	oid  string
	path string
}

func (w *Workspace) listRuleTree(ctx context.Context, repoDir, commit string) ([]treeEntry, error) {
	// 不使用 --long：部分克隆中读取对象大小会逐个触发下载。
	args := append([]string{"ls-tree", "-r", "-z", commit, "--"}, rulePathspecs...)
	output, err := w.gitBytes(ctx, repoDir, nil, args...)
	if err != nil {
		return nil, err
	}
	entries := make([]treeEntry, 0)
	for _, record := range bytes.Split(output, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		meta, name, ok := bytes.Cut(record, []byte{'\t'})
		if !ok {
			continue
		}
		fields := strings.Fields(string(meta))
		if len(fields) != 3 {
			continue
		}
		entries = append(entries, treeEntry{mode: fields[0], kind: fields[1], oid: fields[2], path: string(name)})
	}
	return entries, nil
}

// prefetchBlobs 一次性下载部分克隆中缺失的文件，避免逐个按需下载。失败时忽略，读取时仍会按需下载。
func (w *Workspace) prefetchBlobs(ctx context.Context, repoDir string, oids []string) {
	if len(oids) == 0 {
		return
	}
	input := strings.Join(oids, "\n") + "\n"
	_, _ = w.gitBytes(ctx, repoDir, strings.NewReader(input),
		"-c", "fetch.negotiationAlgorithm=noop", "fetch", "--no-tags", "--no-write-fetch-head",
		"--recurse-submodules=no", "--filter=blob:none", "--stdin", "origin")
}

// readBlobs 通过一个 git cat-file --batch 进程读取多个文件。超过 maxSize 的文件不返回内容。
func (w *Workspace) readBlobs(ctx context.Context, repoDir string, oids []string, maxSize int64) (map[string][]byte, error) {
	blobs := make(map[string][]byte, len(oids))
	if len(oids) == 0 {
		return blobs, nil
	}
	cmd := exec.CommandContext(ctx, w.gitPath, append(w.baseArgs(), "cat-file", "--batch")...)
	cmd.Dir = repoDir
	cmd.Env = w.env()
	cmd.Stdin = strings.NewReader(strings.Join(oids, "\n") + "\n")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	reader := bufio.NewReaderSize(stdout, 64<<10)
	var readErr error
	for range oids {
		header, err := reader.ReadString('\n')
		if err != nil {
			readErr = err
			break
		}
		fields := strings.Fields(header)
		if len(fields) == 2 && fields[1] == "missing" {
			continue
		}
		if len(fields) != 3 {
			readErr = fmt.Errorf("无法解析 git cat-file 输出: %q", header)
			break
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			readErr = err
			break
		}
		if fields[1] != "blob" || size > maxSize {
			if _, err := io.CopyN(io.Discard, reader, size+1); err != nil {
				readErr = err
				break
			}
			continue
		}
		data := make([]byte, size+1)
		if _, err := io.ReadFull(reader, data); err != nil {
			readErr = err
			break
		}
		blobs[fields[0]] = data[:size]
	}
	_, _ = io.Copy(io.Discard, reader)
	if err := cmd.Wait(); err != nil && readErr == nil {
		readErr = fmt.Errorf("git cat-file: %s", lastLines(strings.TrimSpace(stderr.String()), 4))
	}
	return blobs, readErr
}

// ExtractRules 把指定提交中的 AGENTS.md、CLAUDE.md 与 SKILL 目录复制到 dest。
func (w *Workspace) ExtractRules(ctx context.Context, repo, commit, dest string) (*RepoRules, error) {
	repoDir := w.repoDir(repo)
	entries, err := w.listRuleTree(ctx, repoDir, commit)
	if err != nil {
		return nil, err
	}
	rules := &RepoRules{Dir: dest, Commit: commit}
	selected := make([]treeEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.kind != "blob" || (entry.mode != "100644" && entry.mode != "100755") {
			continue
		}
		if len(selected) >= maxRuleFiles {
			rules.Truncated = true
			break
		}
		selected = append(selected, entry)
	}
	if len(selected) == 0 {
		return rules, nil
	}
	oids := make([]string, 0, len(selected))
	for _, entry := range selected {
		oids = append(oids, entry.oid)
	}
	w.prefetchBlobs(ctx, repoDir, oids)
	blobs, err := w.readBlobs(ctx, repoDir, oids, maxRuleFileBytes)
	if err != nil && len(blobs) == 0 {
		return nil, err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, err
	}
	var total int
	for _, entry := range selected {
		data, ok := blobs[entry.oid]
		if !ok {
			rules.Truncated = true
			continue
		}
		if total+len(data) > maxRuleBytes {
			rules.Truncated = true
			break
		}
		target, err := safeJoin(dest, entry.path)
		if err != nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(target, data, 0o444); err != nil {
			return nil, err
		}
		total += len(data)
		if entry.path == "AGENTS.md" || entry.path == "CLAUDE.md" {
			rules.Files = append(rules.Files, entry.path)
		}
		if repoSkillFile.MatchString(entry.path) {
			rules.Skills = append(rules.Skills, repoSkill(entry.path, data, target))
		}
	}
	sortSkills(rules.Skills)
	return rules, nil
}

// ListRepoSkills 拉取仓库默认分支并列出其中的 SKILL，用于在网页中选择。
func (w *Workspace) ListRepoSkills(ctx context.Context, repo string) ([]Skill, error) {
	unlock, err := w.lockRepo(ctx, repo, nil)
	if err != nil {
		return nil, err
	}
	defer unlock()
	repoDir, err := w.ensureClone(ctx, repo, func(string) {})
	if err != nil {
		return nil, err
	}
	branch, err := w.defaultBranch(ctx, repoDir)
	if err != nil {
		return nil, err
	}
	if err := validateBranch(branch); err != nil {
		return nil, err
	}
	local := "refs/remotes/origin/" + branch
	if _, err := w.git(ctx, repoDir, "fetch", "--no-tags", "--filter=blob:none", "origin", "+refs/heads/"+branch+":"+local); err != nil {
		return nil, fmt.Errorf("拉取代码失败: %w", err)
	}
	entries, err := w.listRuleTree(ctx, repoDir, local)
	if err != nil {
		return nil, err
	}
	skillEntries := make([]treeEntry, 0)
	oids := make([]string, 0)
	for _, entry := range entries {
		if entry.kind == "blob" && repoSkillFile.MatchString(entry.path) {
			skillEntries = append(skillEntries, entry)
			oids = append(oids, entry.oid)
		}
	}
	w.prefetchBlobs(ctx, repoDir, oids)
	blobs, err := w.readBlobs(ctx, repoDir, oids, maxRuleFileBytes)
	if err != nil && len(blobs) == 0 && len(oids) > 0 {
		return nil, err
	}
	skills := make([]Skill, 0, len(skillEntries))
	for _, entry := range skillEntries {
		skill := repoSkill(entry.path, blobs[entry.oid], "")
		skill.Source = branch + ": " + skill.Source
		skills = append(skills, skill)
	}
	sortSkills(skills)
	return skills, nil
}

func repoSkill(repoPath string, data []byte, localPath string) Skill {
	match := repoSkillFile.FindStringSubmatch(repoPath)
	meta := parseFrontmatter(data)
	name := meta.Name
	if name == "" && len(match) == 3 {
		name = match[2]
	}
	return Skill{
		Agent:       "repo",
		Name:        name,
		Description: meta.Description,
		Source:      path.Dir(repoPath),
		Path:        localPath,
	}
}

func sortSkills(skills []Skill) {
	sort.SliceStable(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
}

func safeJoin(root, relative string) (string, error) {
	if relative == "" || strings.HasPrefix(relative, "/") || strings.Contains(relative, "\\") {
		return "", errors.New("非法路径")
	}
	for _, part := range strings.Split(relative, "/") {
		if part == "" || part == "." || part == ".." || part == ".git" {
			return "", errors.New("非法路径")
		}
	}
	target := filepath.Join(root, filepath.FromSlash(relative))
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("非法路径")
	}
	return target, nil
}
