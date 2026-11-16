package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/xiaoyumuxi/gh-manager/internal/config"
	"github.com/xiaoyumuxi/gh-manager/internal/server"
	"github.com/xiaoyumuxi/gh-manager/internal/store"
)

func newServeCommand() *cobra.Command {
	var port int
	command := &cobra.Command{
		Use:   "serve",
		Short: "启动本地多维表格网页",
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, err := resolvePaths()
			if err != nil {
				return err
			}
			cfg, err := config.Load(paths.ConfigFile)
			if err != nil {
				return err
			}
			if port > 0 {
				cfg.Server.Port = port
				cfg.Normalize()
				if err := cfg.Validate(); err != nil {
					return err
				}
			}

			db, err := store.Open(paths.DBFile)
			if err != nil {
				return err
			}
			defer db.Close()

			app := server.New(server.Options{
				Store:   db,
				Config:  cfg,
				Paths:   paths,
				Version: version,
			})
			httpServer := &http.Server{
				Addr:              app.Addr(),
				Handler:           app.Handler(),
				ReadHeaderTimeout: 10 * time.Second,
				IdleTimeout:       90 * time.Second,
			}

			errCh := make(chan error, 1)
			go func() {
				errCh <- httpServer.ListenAndServe()
			}()
			fmt.Fprintf(cmd.OutOrStdout(), "GH Manager 已启动: http://%s\n", app.Addr())
			fmt.Fprintf(cmd.OutOrStdout(), "配置: %s\n数据库: %s\n", paths.ConfigFile, paths.DBFile)
			fmt.Fprintln(cmd.OutOrStdout(), "按 Ctrl+C 停止服务")

			select {
			case err := <-errCh:
				if errors.Is(err, http.ErrServerClosed) {
					return nil
				}
				return fmt.Errorf("网页服务启动失败: %w", err)
			case <-cmd.Context().Done():
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := httpServer.Shutdown(shutdownCtx); err != nil {
					return fmt.Errorf("关闭网页服务失败: %w", err)
				}
				return nil
			}
		},
	}
	command.Flags().IntVar(&port, "port", 0, "覆盖配置文件中的端口")
	return command
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "显示版本信息",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "gh-manager %s\ncommit: %s\nbuilt: %s\n", version, commit, date)
		},
	}
}
