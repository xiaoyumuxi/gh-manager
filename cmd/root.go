package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/xiaoyumuxi/gh-manager/internal/config"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"

	homeOverride string
)

var rootCmd = &cobra.Command{
	Use:           "gh-manager",
	Short:         "跨仓库管理 GitHub Issue 与 Pull Request",
	Long:          "GH Manager 使用 GitHub CLI 的登录状态同步多个仓库的 Issue 和 Pull Request，并在本地网页中管理标签与自定义字段。",
	SilenceErrors: true,
	SilenceUsage:  true,
	Version:       version,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

func Execute() {
	rootCmd.Version = version
	rootCmd.SetVersionTemplate(fmt.Sprintf("gh-manager %s (commit %s, built %s)\n", version, commit, date))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&homeOverride, "home", "", "配置与数据库目录（也可使用 GH_MANAGER_HOME）")
	rootCmd.AddCommand(newInitCommand())
	rootCmd.AddCommand(newSyncCommand())
	rootCmd.AddCommand(newServeCommand())
	rootCmd.AddCommand(newVersionCommand())
}

func resolvePaths() (config.Paths, error) {
	return config.ResolvePaths(homeOverride)
}
