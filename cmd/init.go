package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/xiaoyumuxi/gh-manager/internal/config"
	"github.com/xiaoyumuxi/gh-manager/internal/githubsync"
)

func newInitCommand() *cobra.Command {
	var (
		org    string
		repos  []string
		force  bool
		port   int
		stale  int
		review int
		closed int
		page   int
	)
	command := &cobra.Command{
		Use:   "init",
		Short: "创建配置文件并选择要跟踪的仓库",
		Long:  "从组织或用户获取未归档仓库，也可以使用 --repos 直接指定仓库。",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, err := resolvePaths()
			if err != nil {
				return err
			}
			if !force {
				if _, err := os.Stat(paths.ConfigFile); err == nil {
					return fmt.Errorf("配置文件已存在: %s（使用 --force 覆盖）", paths.ConfigFile)
				} else if !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("检查配置文件失败: %w", err)
				}
			}

			allRepos := append([]string{}, args...)
			allRepos = append(allRepos, repos...)
			allRepos = splitRepos(allRepos)

			org = strings.TrimSpace(org)
			if len(allRepos) == 0 && org == "" {
				return errors.New("请使用 --org <组织或用户> 获取仓库，或用 --repos owner/repo 指定仓库")
			}
			if len(allRepos) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "正在获取 %s 的仓库列表...\n", org)
				allRepos, err = githubsync.ListRepositories(cmd.Context(), org)
				if err != nil {
					return fmt.Errorf("获取仓库列表失败: %w", err)
				}
				if len(allRepos) == 0 {
					return fmt.Errorf("%s 下没有可用的未归档仓库", org)
				}
			}

			cfg := config.Default()
			cfg.Repos = allRepos
			if port > 0 {
				cfg.Server.Port = port
			}
			if stale > 0 {
				cfg.Sync.StaleDays = stale
			}
			if review > 0 {
				cfg.Sync.ReviewStaleDays = review
			}
			if closed > 0 {
				cfg.Sync.ClosedWindowDays = closed
			}
			if page > 0 {
				cfg.Sync.RequestPageSize = page
			}
			if err := config.Save(paths.ConfigFile, cfg); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "配置已写入 %s\n", paths.ConfigFile)
			fmt.Fprintf(cmd.OutOrStdout(), "已添加 %d 个仓库：\n", len(cfg.Repos))
			for _, repo := range cfg.Repos {
				fmt.Fprintf(cmd.OutOrStdout(), "  - %s\n", repo)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "下一步运行: gh-manager sync")
			return nil
		},
	}
	command.Flags().StringVar(&org, "org", "", "获取该组织或用户下的未归档仓库")
	command.Flags().StringSliceVar(&repos, "repos", nil, "直接指定仓库，可重复或使用逗号分隔")
	command.Flags().BoolVar(&force, "force", false, "覆盖已有配置")
	command.Flags().IntVar(&port, "port", 0, "网页服务端口")
	command.Flags().IntVar(&stale, "stale-days", 0, "超过多少天未更新视为停滞")
	command.Flags().IntVar(&review, "review-stale-days", 0, "等待评审多少天后视为超时")
	command.Flags().IntVar(&closed, "closed-window-days", 0, "同步最近多少天关闭的数据")
	command.Flags().IntVar(&page, "page-size", 0, "GitHub API 每页数量（1-100）")
	return command
}

func splitRepos(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		for _, repo := range strings.FieldsFunc(value, func(r rune) bool {
			return r == ',' || r == '\n' || r == '\r' || r == '\t' || r == ' '
		}) {
			repo = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(repo, "https://github.com/"), ".git"))
			repo = strings.Trim(repo, "/")
			if repo == "" {
				continue
			}
			key := strings.ToLower(repo)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, repo)
		}
	}
	return result
}
