package agent

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// CLIStatus 描述本机某个命令行工具是否可用。
type CLIStatus struct {
	Name      string `json:"name"`
	Command   string `json:"command"`
	Path      string `json:"path"`
	Version   string `json:"version"`
	Available bool   `json:"available"`
	Error     string `json:"error"`
}

// Skill 是从本机配置目录中发现的 SKILL 或自定义命令。
type Skill struct {
	Agent       string `json:"agent"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	// Path 是仓库 SKILL 在本机只读副本中的路径，仅运行时使用。
	Path string `json:"-"`
}

var (
	detectMu    sync.Mutex
	detectCache map[string]CLIStatus
	detectAt    time.Time
)

// ResolveCommand 返回命令的绝对路径。configured 为空时在 PATH 中查找 fallback。
func ResolveCommand(configured, fallback string) (string, error) {
	command := strings.TrimSpace(configured)
	if command == "" {
		command = fallback
	}
	if strings.HasPrefix(command, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			command = filepath.Join(home, command[2:])
		}
	}
	return exec.LookPath(command)
}

// DetectCLIs 检查 claude / codex / gh / git 是否可用，结果缓存 30 秒。
func DetectCLIs(ctx context.Context, settings Settings, force bool) []CLIStatus {
	detectMu.Lock()
	defer detectMu.Unlock()
	key := settings.ClaudeCommand + "\x00" + settings.CodexCommand
	if !force && detectCache != nil && time.Since(detectAt) < 30*time.Second && detectCache["__key"].Command == key {
		return orderedStatuses(detectCache)
	}
	specs := []struct{ name, configured, fallback string }{
		{"claude", settings.ClaudeCommand, "claude"},
		{"codex", settings.CodexCommand, "codex"},
		{"gh", "", "gh"},
		{"git", "", "git"},
	}
	results := make(map[string]CLIStatus, len(specs)+1)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, spec := range specs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status := CLIStatus{Name: spec.name, Command: spec.fallback}
			if spec.configured != "" {
				status.Command = spec.configured
			}
			path, err := ResolveCommand(spec.configured, spec.fallback)
			if err != nil {
				status.Error = "未找到命令，请确认已安装并在 PATH 中，或在设置中填写完整路径"
			} else {
				status.Path = path
				versionCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
				output, err := exec.CommandContext(versionCtx, path, "--version").CombinedOutput()
				cancel()
				if err != nil {
					status.Error = "无法执行 --version: " + err.Error()
				} else {
					status.Available = true
					status.Version = firstLine(string(output))
				}
			}
			mu.Lock()
			results[spec.name] = status
			mu.Unlock()
		}()
	}
	wg.Wait()
	results["__key"] = CLIStatus{Command: key}
	detectCache = results
	detectAt = time.Now()
	return orderedStatuses(results)
}

func orderedStatuses(results map[string]CLIStatus) []CLIStatus {
	ordered := make([]CLIStatus, 0, 4)
	for _, name := range []string{"claude", "codex", "gh", "git"} {
		if status, ok := results[name]; ok {
			ordered = append(ordered, status)
		}
	}
	return ordered
}

func firstLine(text string) string {
	text = strings.TrimSpace(text)
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		text = text[:index]
	}
	if len(text) > 120 {
		text = text[:120]
	}
	return text
}

var versionDir = regexp.MustCompile(`^(v?\d[\w.\-+]*|[0-9a-f]{8,})$`)

// DiscoverSkills 扫描 Claude Code 与 Codex 的用户级 SKILL 目录。
func DiscoverSkills() []Skill {
	home, err := os.UserHomeDir()
	if err != nil {
		return []Skill{}
	}
	skills := make([]Skill, 0)
	seen := make(map[string]bool)
	add := func(skill Skill) {
		key := skill.Agent + "\x00" + skill.Name
		if skill.Name == "" || seen[key] {
			return
		}
		seen[key] = true
		skills = append(skills, skill)
	}

	claudeHome := filepath.Join(home, ".claude")
	if value := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); value != "" {
		claudeHome = value
	}
	for _, skill := range scanSkillDir(filepath.Join(claudeHome, "skills")) {
		skill.Agent, skill.Source = "claude", "用户 SKILL"
		add(skill)
	}
	if entries, err := os.ReadDir(filepath.Join(claudeHome, "commands")); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			name := strings.TrimSuffix(entry.Name(), ".md")
			meta := readFrontmatter(filepath.Join(claudeHome, "commands", entry.Name()))
			add(Skill{Agent: "claude", Name: name, Description: meta.Description, Source: "自定义命令"})
		}
	}
	for _, skill := range scanPluginSkills(filepath.Join(claudeHome, "plugins")) {
		add(skill)
	}

	codexHome := filepath.Join(home, ".codex")
	if value := strings.TrimSpace(os.Getenv("CODEX_HOME")); value != "" {
		codexHome = value
	}
	for _, dir := range []string{filepath.Join(codexHome, "skills"), filepath.Join(home, ".agents", "skills")} {
		for _, skill := range scanSkillDir(dir) {
			skill.Agent, skill.Source = "codex", "用户 SKILL"
			add(skill)
		}
	}
	sort.SliceStable(skills, func(i, j int) bool {
		if skills[i].Agent != skills[j].Agent {
			return skills[i].Agent < skills[j].Agent
		}
		return skills[i].Name < skills[j].Name
	})
	return skills
}

func scanSkillDir(dir string) []Skill {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	skills := make([]Skill, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			continue
		}
		path := filepath.Join(dir, entry.Name(), "SKILL.md")
		if _, err := os.Stat(path); err != nil {
			continue
		}
		meta := readFrontmatter(path)
		name := meta.Name
		if name == "" {
			name = entry.Name()
		}
		skills = append(skills, Skill{Name: name, Description: meta.Description})
	}
	return skills
}

func scanPluginSkills(root string) []Skill {
	skills := make([]Skill, 0)
	rootDepth := strings.Count(filepath.Clean(root), string(filepath.Separator))
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		depth := strings.Count(path, string(filepath.Separator)) - rootDepth
		if entry.IsDir() {
			if depth > 7 || strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() != "SKILL.md" {
			return nil
		}
		skillDir := filepath.Dir(path)
		skillsDir := filepath.Dir(skillDir)
		if filepath.Base(skillsDir) != "skills" {
			return nil
		}
		pluginDir := filepath.Dir(skillsDir)
		plugin := filepath.Base(pluginDir)
		if versionDir.MatchString(plugin) {
			plugin = filepath.Base(filepath.Dir(pluginDir))
		}
		meta := readFrontmatter(path)
		name := meta.Name
		if name == "" {
			name = filepath.Base(skillDir)
		}
		skills = append(skills, Skill{
			Agent: "claude", Name: plugin + ":" + name, Description: meta.Description, Source: "插件 " + plugin,
		})
		return nil
	})
	return skills
}

type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

func readFrontmatter(path string) frontmatter {
	file, err := os.Open(path)
	if err != nil {
		return frontmatter{}
	}
	defer file.Close()
	buffer := make([]byte, 16<<10)
	n, _ := io.ReadFull(file, buffer)
	return parseFrontmatter(buffer[:n])
}

func parseFrontmatter(data []byte) frontmatter {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !bytes.HasPrefix(data, []byte("---")) {
		return frontmatter{}
	}
	rest := data[3:]
	end := bytes.Index(rest, []byte("\n---"))
	if end < 0 {
		return frontmatter{}
	}
	var meta frontmatter
	_ = yaml.Unmarshal(rest[:end], &meta)
	meta.Name = strings.TrimSpace(meta.Name)
	meta.Description = strings.TrimSpace(meta.Description)
	if len([]rune(meta.Description)) > 200 {
		meta.Description = string([]rune(meta.Description)[:200]) + "…"
	}
	return meta
}
