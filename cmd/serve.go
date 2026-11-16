package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/xiaoyumuxi/gh-manager/internal/config"
	"github.com/xiaoyumuxi/gh-manager/internal/server"
	"github.com/xiaoyumuxi/gh-manager/internal/store"
)

type serveOptions struct {
	port int
	open bool
}

func newServeCommand() *cobra.Command {
	options := &serveOptions{}
	command := &cobra.Command{
		Use:   "serve",
		Short: "启动本地网页（默认命令）",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd, options)
		},
	}
	bindServeFlags(command, options)
	return command
}

func bindServeFlags(command *cobra.Command, options *serveOptions) {
	command.Flags().IntVar(&options.port, "port", 0, "覆盖配置文件中的端口")
	command.Flags().BoolVar(&options.open, "open", false, "启动后自动打开浏览器")
}

func runServe(cmd *cobra.Command, options *serveOptions) error {
	paths, err := resolvePaths()
	if err != nil {
		return err
	}
	cfg, created, err := config.LoadOrCreate(paths.ConfigFile)
	if err != nil {
		return err
	}
	if created {
		fmt.Fprintf(cmd.OutOrStdout(), "已创建默认配置: %s（可在网页中添加仓库）\n", paths.ConfigFile)
	}
	if options.port > 0 {
		cfg.Server.Port = options.port
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

	app, err := server.New(server.Options{
		Store:   db,
		Config:  cfg,
		Paths:   paths,
		Version: version,
	})
	if err != nil {
		return err
	}
	defer app.Close()

	httpServer := &http.Server{
		Addr:              app.Addr(),
		Handler:           app.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	httpServer.RegisterOnShutdown(app.StopStreams)

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.ListenAndServe()
	}()
	url := "http://" + app.Addr()
	fmt.Fprintf(cmd.OutOrStdout(), "GH Manager 已启动: %s\n", url)
	fmt.Fprintf(cmd.OutOrStdout(), "配置: %s\n数据库: %s\n", paths.ConfigFile, paths.DBFile)
	fmt.Fprintln(cmd.OutOrStdout(), "按 Ctrl+C 停止服务")
	if options.open {
		go func() {
			time.Sleep(300 * time.Millisecond)
			openBrowser(url)
		}()
	}

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
}

func openBrowser(url string) {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", url)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		command = exec.Command("xdg-open", url)
	}
	_ = command.Start()
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
