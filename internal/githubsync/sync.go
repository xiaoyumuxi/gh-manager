package githubsync

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/xiaoyumuxi/gh-manager/internal/config"
	"github.com/xiaoyumuxi/gh-manager/internal/store"
)

var ErrSyncBusy = errors.New("已有同步任务正在运行，请稍后再试")

type SyncOptions struct {
	Store            *store.Store
	Repos            []string
	PageSize         int
	ClosedWindowDays int
	Progress         func(string)
}

type Summary struct {
	Repos  int
	Items  int
	Errors []string
}

func Run(ctx context.Context, options SyncOptions) (Summary, error) {
	if options.Store == nil {
		return Summary{}, errors.New("数据库未初始化")
	}
	if len(options.Repos) == 0 {
		return Summary{}, errors.New("配置中没有仓库，请编辑 config.yml 或运行 gh-manager init --org <owner>")
	}
	if options.PageSize <= 0 || options.PageSize > 100 {
		options.PageSize = 100
	}
	if options.ClosedWindowDays <= 0 {
		options.ClosedWindowDays = 30
	}

	owner := randomOwner()
	locked, err := options.Store.AcquireSyncLock(ctx, owner)
	if err != nil {
		return Summary{}, fmt.Errorf("获取同步锁失败: %w", err)
	}
	if !locked {
		return Summary{}, ErrSyncBusy
	}
	defer options.Store.ReleaseSyncLock(context.Background(), owner)

	client, err := NewClient()
	if err != nil {
		return Summary{}, err
	}
	runID, err := options.Store.StartSyncRun(ctx, len(options.Repos))
	if err != nil {
		return Summary{}, fmt.Errorf("记录同步任务失败: %w", err)
	}
	summary := Summary{Repos: len(options.Repos)}
	progress := options.Progress
	if progress == nil {
		progress = func(string) {}
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -options.ClosedWindowDays)
	successfulRepos := 0
	for index, fullName := range options.Repos {
		if err := ctx.Err(); err != nil {
			summary.Errors = append(summary.Errors, err.Error())
			break
		}
		ownerName, repoName, err := config.SplitRepo(fullName)
		if err != nil {
			summary.Errors = append(summary.Errors, err.Error())
			_ = options.Store.SetRepoResult(ctx, fullName, err.Error())
			continue
		}
		progress(fmt.Sprintf("[%d/%d] 同步 %s", index+1, len(options.Repos), fullName))
		boundary := time.Now().UTC()

		items := make([]store.GitHubItem, 0)
		var fetchErrors []string

		openIssues, complete, err := client.listIssues(ctx, ownerName, repoName, options.PageSize, false, cutoff)
		fetchErrors = appendFetchError(fetchErrors, "issue/open", err)
		openIssuesComplete := complete && err == nil
		items = append(items, openIssues...)

		closedIssues, _, err := client.listIssues(ctx, ownerName, repoName, options.PageSize, true, cutoff)
		fetchErrors = appendFetchError(fetchErrors, "issue/closed", err)
		items = append(items, closedIssues...)

		openPulls, complete, err := client.listPullRequests(ctx, ownerName, repoName, options.PageSize, false, time.Time{})
		fetchErrors = appendFetchError(fetchErrors, "pr/open", err)
		openPullsComplete := complete && err == nil
		items = append(items, openPulls...)

		closedPulls, _, err := client.listPullRequests(ctx, ownerName, repoName, options.PageSize, true, cutoff)
		fetchErrors = appendFetchError(fetchErrors, "pr/closed", err)
		items = append(items, closedPulls...)

		if len(items) > 0 || (openIssuesComplete && openPullsComplete) {
			count, applyErr := options.Store.ApplyRepoSnapshot(
				ctx, fullName, items, openIssuesComplete && openPullsComplete, boundary,
			)
			if applyErr != nil {
				fetchErrors = append(fetchErrors, "写入本地数据库: "+applyErr.Error())
			} else {
				summary.Items += count
			}
		}

		if len(fetchErrors) > 0 {
			errorText := strings.Join(fetchErrors, "; ")
			summary.Errors = append(summary.Errors, fullName+": "+errorText)
			_ = options.Store.SetRepoResult(ctx, fullName, errorText)
		} else {
			successfulRepos++
			_ = options.Store.SetRepoResult(ctx, fullName, "")
		}
	}

	status := "success"
	errorText := strings.Join(summary.Errors, "\n")
	if len(summary.Errors) > 0 {
		if successfulRepos == 0 {
			status = "error"
		} else {
			status = "partial"
		}
	}
	if err := options.Store.FinishSyncRun(context.Background(), runID, status, errorText, summary.Items); err != nil {
		summary.Errors = append(summary.Errors, "记录同步结果失败: "+err.Error())
	}
	progress(fmt.Sprintf("同步完成：%d 个仓库，%d 条记录", successfulRepos, summary.Items))
	if len(summary.Errors) > 0 {
		return summary, errors.New(errorText)
	}
	return summary, nil
}

func appendFetchError(errorsList []string, section string, err error) []string {
	if err != nil {
		return append(errorsList, section+": "+err.Error())
	}
	return errorsList
}

func randomOwner() string {
	data := make([]byte, 12)
	if _, err := rand.Read(data); err != nil {
		return fmt.Sprintf("sync-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(data)
}
