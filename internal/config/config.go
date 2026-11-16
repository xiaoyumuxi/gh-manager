package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const configVersion = 1

type SyncConfig struct {
	StaleDays        int `yaml:"stale_days" json:"stale_days"`
	ReviewStaleDays  int `yaml:"review_stale_days" json:"review_stale_days"`
	ClosedWindowDays int `yaml:"closed_window_days" json:"closed_window_days"`
	RequestPageSize  int `yaml:"request_page_size" json:"request_page_size"`
}

type ServerConfig struct {
	Port int `yaml:"port"`
}

type AgentConfig struct {
	ClaudeCommand  string `yaml:"claude_command,omitempty" json:"claude_command"`
	CodexCommand   string `yaml:"codex_command,omitempty" json:"codex_command"`
	ClaudeModel    string `yaml:"claude_model,omitempty" json:"claude_model"`
	CodexModel     string `yaml:"codex_model,omitempty" json:"codex_model"`
	MaxConcurrent  int    `yaml:"max_concurrent" json:"max_concurrent"`
	TimeoutMinutes int    `yaml:"timeout_minutes" json:"timeout_minutes"`
	WorkspaceDir   string `yaml:"workspace_dir,omitempty" json:"workspace_dir"`
}

type RepoConfig struct {
	Name   string `yaml:"name" json:"name"`
	Branch string `yaml:"branch,omitempty" json:"branch,omitempty"`
}

func (r *RepoConfig) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		r.Name = node.Value
		r.Branch = ""
		return nil
	case yaml.MappingNode:
		type plain RepoConfig
		var value plain
		if err := node.Decode(&value); err != nil {
			return err
		}
		*r = RepoConfig(value)
		return nil
	default:
		return fmt.Errorf("仓库配置必须是 owner/name 字符串或包含 name 的对象")
	}
}

type Config struct {
	Version int          `yaml:"version"`
	Repos   []RepoConfig `yaml:"repos"`
	Sync    SyncConfig   `yaml:"sync"`
	Server  ServerConfig `yaml:"server"`
	Agent   AgentConfig  `yaml:"agent"`
}

type Paths struct {
	Home         string
	ConfigFile   string
	DBFile       string
	WorkspaceDir string
	RunsDir      string
}

func Default() *Config {
	return &Config{
		Version: configVersion,
		Repos:   []RepoConfig{},
		Sync: SyncConfig{
			StaleDays:        14,
			ReviewStaleDays:  3,
			ClosedWindowDays: 30,
			RequestPageSize:  100,
		},
		Server: ServerConfig{Port: 8787},
		Agent: AgentConfig{
			MaxConcurrent:  2,
			TimeoutMinutes: 30,
		},
	}
}

func ResolvePaths(homeOverride string) (Paths, error) {
	home := strings.TrimSpace(homeOverride)
	if home == "" {
		home = strings.TrimSpace(os.Getenv("GH_MANAGER_HOME"))
	}
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, fmt.Errorf("无法确定用户目录: %w", err)
		}
		home = filepath.Join(userHome, ".config", "gh-manager")
	}
	expanded, err := expandHome(home)
	if err != nil {
		return Paths{}, err
	}
	absolute, err := filepath.Abs(expanded)
	if err != nil {
		return Paths{}, fmt.Errorf("无法解析配置目录: %w", err)
	}
	return Paths{
		Home:         absolute,
		ConfigFile:   filepath.Join(absolute, "config.yml"),
		DBFile:       filepath.Join(absolute, "gh-manager.db"),
		WorkspaceDir: filepath.Join(absolute, "workspaces"),
		RunsDir:      filepath.Join(absolute, "runs"),
	}, nil
}

// LoadOrCreate 读取配置；文件不存在时写入默认配置，便于直接通过网页完成初始化。
func LoadOrCreate(path string) (*Config, bool, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		cfg := Default()
		if err := Save(path, cfg); err != nil {
			return nil, false, err
		}
		return cfg, true, nil
	}
	cfg, err := Load(path)
	return cfg, false, err
}

// ResolveWorkspaceDir 返回 Agent 工作区目录，未配置时使用默认目录。
func (p Paths) ResolveWorkspaceDir(cfg *Config) string {
	if cfg != nil && strings.TrimSpace(cfg.Agent.WorkspaceDir) != "" {
		if expanded, err := expandHome(strings.TrimSpace(cfg.Agent.WorkspaceDir)); err == nil {
			if absolute, err := filepath.Abs(expanded); err == nil {
				return absolute
			}
		}
	}
	return p.WorkspaceDir
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("配置文件不存在: %s", path)
		}
		return nil, fmt.Errorf("读取配置失败: %w", err)
	}
	cfg := Default()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func Save(path string, cfg *Config) error {
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("生成配置失败: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("写入配置失败: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("保存配置失败: %w", err)
	}
	return nil
}

func (c *Config) Normalize() {
	if c.Version == 0 {
		c.Version = configVersion
	}
	seen := make(map[string]struct{}, len(c.Repos))
	repos := make([]RepoConfig, 0, len(c.Repos))
	for _, repo := range c.Repos {
		repo.Name = NormalizeRepoName(repo.Name)
		repo.Branch = NormalizeBranchName(repo.Branch)
		if repo.Name == "" {
			continue
		}
		key := strings.ToLower(repo.Name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		repos = append(repos, repo)
	}
	c.Repos = repos
	if c.Sync.StaleDays <= 0 {
		c.Sync.StaleDays = 14
	}
	if c.Sync.ReviewStaleDays <= 0 {
		c.Sync.ReviewStaleDays = 3
	}
	if c.Sync.ClosedWindowDays <= 0 {
		c.Sync.ClosedWindowDays = 30
	}
	if c.Sync.RequestPageSize <= 0 {
		c.Sync.RequestPageSize = 100
	}
	if c.Sync.RequestPageSize > 100 {
		c.Sync.RequestPageSize = 100
	}
	if c.Server.Port == 0 {
		c.Server.Port = 8787
	}
	c.Agent.ClaudeCommand = strings.TrimSpace(c.Agent.ClaudeCommand)
	c.Agent.CodexCommand = strings.TrimSpace(c.Agent.CodexCommand)
	c.Agent.ClaudeModel = strings.TrimSpace(c.Agent.ClaudeModel)
	c.Agent.CodexModel = strings.TrimSpace(c.Agent.CodexModel)
	c.Agent.WorkspaceDir = strings.TrimSpace(c.Agent.WorkspaceDir)
	if c.Agent.MaxConcurrent <= 0 {
		c.Agent.MaxConcurrent = 2
	}
	if c.Agent.MaxConcurrent > 8 {
		c.Agent.MaxConcurrent = 8
	}
	if c.Agent.TimeoutMinutes <= 0 {
		c.Agent.TimeoutMinutes = 30
	}
	if c.Agent.TimeoutMinutes > 240 {
		c.Agent.TimeoutMinutes = 240
	}
}

func (c *Config) Validate() error {
	if c.Version != configVersion {
		return fmt.Errorf("不支持的配置版本 %d，当前支持 %d", c.Version, configVersion)
	}
	for _, repo := range c.Repos {
		if _, _, err := SplitRepo(repo.Name); err != nil {
			return err
		}
		if err := ValidateBranchName(repo.Branch); err != nil {
			return fmt.Errorf("仓库 %s 的追踪分支无效: %w", repo.Name, err)
		}
	}
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("端口必须在 1-65535 之间: %d", c.Server.Port)
	}
	for _, model := range []string{c.Agent.ClaudeModel, c.Agent.CodexModel} {
		if strings.ContainsAny(model, " \t\r\n") || strings.HasPrefix(model, "-") {
			return fmt.Errorf("模型名无效: %q", model)
		}
	}
	return nil
}

func SplitRepo(fullName string) (string, string, error) {
	parts := strings.Split(strings.TrimSpace(fullName), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("无效仓库名: %q", fullName)
	}
	return parts[0], parts[1], nil
}

func NormalizeRepoName(fullName string) string {
	fullName = strings.TrimSpace(fullName)
	fullName = strings.TrimPrefix(fullName, "https://github.com/")
	fullName = strings.TrimPrefix(fullName, "http://github.com/")
	fullName = strings.TrimPrefix(fullName, "github.com/")
	fullName = strings.TrimPrefix(fullName, "git@github.com:")
	fullName = strings.TrimSuffix(fullName, ".git")
	return strings.Trim(fullName, "/")
}

func NormalizeBranchName(branch string) string {
	branch = strings.TrimSpace(branch)
	branch = strings.TrimPrefix(branch, "refs/heads/")
	return strings.Trim(branch, "/")
}

func ValidateBranchName(branch string) error {
	if branch == "" {
		return nil
	}
	if strings.ContainsAny(branch, " \t\r\n\\") {
		return errors.New("分支名不能包含空白或反斜杠")
	}
	if strings.Contains(branch, "..") || strings.Contains(branch, "@{") {
		return errors.New("分支名包含无效字符")
	}
	if strings.HasPrefix(branch, "-") || strings.HasSuffix(branch, ".") {
		return errors.New("分支名不能以 - 开头或以 . 结尾")
	}
	return nil
}

func expandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法确定用户目录: %w", err)
	}
	if path == "~" {
		return userHome, nil
	}
	return filepath.Join(userHome, path[2:]), nil
}
