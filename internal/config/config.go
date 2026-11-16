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
	StaleDays        int `yaml:"stale_days"`
	ReviewStaleDays  int `yaml:"review_stale_days"`
	ClosedWindowDays int `yaml:"closed_window_days"`
	RequestPageSize  int `yaml:"request_page_size"`
}

type ServerConfig struct {
	Port int `yaml:"port"`
}

type Config struct {
	Version int          `yaml:"version"`
	Repos   []string     `yaml:"repos"`
	Sync    SyncConfig   `yaml:"sync"`
	Server  ServerConfig `yaml:"server"`
}

type Paths struct {
	Home       string
	ConfigFile string
	DBFile     string
}

func Default() *Config {
	return &Config{
		Version: configVersion,
		Repos:   []string{},
		Sync: SyncConfig{
			StaleDays:        14,
			ReviewStaleDays:  3,
			ClosedWindowDays: 30,
			RequestPageSize:  100,
		},
		Server: ServerConfig{Port: 8787},
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
		Home:       absolute,
		ConfigFile: filepath.Join(absolute, "config.yml"),
		DBFile:     filepath.Join(absolute, "gh-manager.db"),
	}, nil
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("配置文件不存在，请先运行 gh-manager init: %s", path)
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
	repos := make([]string, 0, len(c.Repos))
	for _, repo := range c.Repos {
		repo = strings.TrimSpace(repo)
		repo = strings.TrimPrefix(repo, "https://github.com/")
		repo = strings.TrimSuffix(repo, ".git")
		repo = strings.Trim(repo, "/")
		if repo == "" {
			continue
		}
		key := strings.ToLower(repo)
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
}

func (c *Config) Validate() error {
	if c.Version != configVersion {
		return fmt.Errorf("不支持的配置版本 %d，当前支持 %d", c.Version, configVersion)
	}
	for _, repo := range c.Repos {
		parts := strings.Split(repo, "/")
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return fmt.Errorf("仓库格式应为 owner/name: %q", repo)
		}
	}
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("端口必须在 1-65535 之间: %d", c.Server.Port)
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
