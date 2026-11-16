package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/xiaoyumuxi/gh-manager/internal/config"
	"github.com/xiaoyumuxi/gh-manager/internal/githubsync"
	"github.com/xiaoyumuxi/gh-manager/internal/store"
)

func newSyncCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "sync [owner/repo...]",
		Short: "同步 GitHub Issue 与 Pull Request 到本地数据库",
		Long:  "不传仓库参数时使用 config.yml 中的仓库列表；传入时仅同步本次指定的仓库。",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, err := resolvePaths()
			if err != nil {
				return err
			}
			cfg, err := config.Load(paths.ConfigFile)
			if err != nil {
				return err
			}
			repos := cfg.Repos
			if len(args) > 0 {
				repos = splitRepos(args)
			}
			if len(repos) == 0 {
				return fmt.Errorf("没有可同步的仓库，请先运行 gh-manager init")
			}

			db, err := store.Open(paths.DBFile)
			if err != nil {
				return err
			}
			defer db.Close()

			summary, runErr := githubsync.Run(cmd.Context(), githubsync.SyncOptions{
				Store:            db,
				Repos:            repos,
				PageSize:         cfg.Sync.RequestPageSize,
				ClosedWindowDays: cfg.Sync.ClosedWindowDays,
				Progress: func(message string) {
					fmt.Fprintln(cmd.OutOrStdout(), message)
				},
			})
			if runErr != nil {
				return runErr
			}
			fmt.Fprintf(cmd.OutOrStdout(), "已同步 %d 个仓库、%d 条 GitHub 记录，数据保存在 %s\n",
				summary.Repos, summary.Items, paths.DBFile)
			return nil
		},
	}
}
