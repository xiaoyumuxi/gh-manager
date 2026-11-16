package githubsync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/xiaoyumuxi/gh-manager/internal/config"
)

const viewerQuery = `query { viewer { login databaseId name } }`

const pullDetailQuery = `
query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    viewerPermission
    pullRequest(number: $number) {
      number
      title
      url
      state
      isDraft
      body
      bodyHTML
      createdAt
      updatedAt
      author { login avatarUrl }
      headRefName
      headRefOid
      baseRefName
      baseRefOid
      isCrossRepository
      maintainerCanModify
      headRepository { nameWithOwner }
      additions
      deletions
      changedFiles
      mergeable
      reviewDecision
      comments(last: 50) {
        totalCount
        nodes { author { login avatarUrl } body bodyHTML createdAt url }
      }
      reviews(last: 30) {
        nodes { author { login avatarUrl } state body bodyHTML submittedAt url }
      }
      reviewThreads(last: 50) {
        nodes {
          isResolved
          isOutdated
          path
          line
          comments(first: 10) {
            nodes { author { login avatarUrl } body bodyHTML createdAt url }
          }
        }
      }
      files(first: 100) {
        totalCount
        nodes { path additions deletions changeType }
      }
      commits(last: 1) {
        nodes {
          commit {
            oid
            statusCheckRollup {
              state
              contexts(first: 60) {
                nodes {
                  __typename
                  ... on CheckRun { name status conclusion detailsUrl }
                  ... on StatusContext { context state targetUrl }
                }
              }
            }
          }
        }
      }
    }
  }
}`

const issueDetailQuery = `
query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    viewerPermission
    issue(number: $number) {
      number
      title
      url
      state
      body
      bodyHTML
      createdAt
      updatedAt
      author { login avatarUrl }
      comments(last: 50) {
        totalCount
        nodes { author { login avatarUrl } body bodyHTML createdAt url }
      }
    }
  }
}`

type Viewer struct {
	Login      string `json:"login"`
	DatabaseID int64  `json:"databaseId"`
	Name       string `json:"name"`
}

type Person struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatar_url"`
}

type Comment struct {
	Author    Person `json:"author"`
	Body      string `json:"body"`
	BodyHTML  string `json:"body_html"`
	CreatedAt string `json:"created_at"`
	URL       string `json:"url"`
}

type Review struct {
	Author      Person `json:"author"`
	State       string `json:"state"`
	Body        string `json:"body"`
	BodyHTML    string `json:"body_html"`
	SubmittedAt string `json:"submitted_at"`
	URL         string `json:"url"`
}

type ReviewThread struct {
	IsResolved bool      `json:"is_resolved"`
	IsOutdated bool      `json:"is_outdated"`
	Path       string    `json:"path"`
	Line       int       `json:"line"`
	Comments   []Comment `json:"comments"`
}

type ChangedFile struct {
	Path       string `json:"path"`
	Additions  int    `json:"additions"`
	Deletions  int    `json:"deletions"`
	ChangeType string `json:"change_type"`
}

type Check struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	URL        string `json:"url"`
}

// Detail 是按需从 GitHub 拉取的完整条目信息。
type Detail struct {
	Repo                string         `json:"repo"`
	Number              int            `json:"number"`
	Kind                string         `json:"kind"`
	Title               string         `json:"title"`
	URL                 string         `json:"url"`
	State               string         `json:"state"`
	IsDraft             bool           `json:"is_draft"`
	Body                string         `json:"body"`
	BodyHTML            string         `json:"body_html"`
	Author              Person         `json:"author"`
	CreatedAt           string         `json:"created_at"`
	UpdatedAt           string         `json:"updated_at"`
	ViewerPermission    string         `json:"viewer_permission"`
	HeadRef             string         `json:"head_ref"`
	HeadOID             string         `json:"head_oid"`
	HeadRepo            string         `json:"head_repo"`
	BaseRef             string         `json:"base_ref"`
	BaseOID             string         `json:"base_oid"`
	IsCrossRepository   bool           `json:"is_cross_repository"`
	MaintainerCanModify bool           `json:"maintainer_can_modify"`
	Additions           int            `json:"additions"`
	Deletions           int            `json:"deletions"`
	ChangedFiles        int            `json:"changed_files"`
	Mergeable           string         `json:"mergeable"`
	ReviewDecision      string         `json:"review_decision"`
	CommentCount        int            `json:"comment_count"`
	Comments            []Comment      `json:"comments"`
	Reviews             []Review       `json:"reviews"`
	ReviewThreads       []ReviewThread `json:"review_threads"`
	Files               []ChangedFile  `json:"files"`
	FileCount           int            `json:"file_count"`
	CheckState          string         `json:"check_state"`
	Checks              []Check        `json:"checks"`
	FetchedAt           string         `json:"fetched_at"`
}

type authorNode struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatarUrl"`
}

type commentNode struct {
	Author    *authorNode `json:"author"`
	Body      string      `json:"body"`
	BodyHTML  string      `json:"bodyHTML"`
	CreatedAt string      `json:"createdAt"`
	URL       string      `json:"url"`
}

type commentConnection struct {
	TotalCount int           `json:"totalCount"`
	Nodes      []commentNode `json:"nodes"`
}

type pullDetailResponse struct {
	Repository *struct {
		ViewerPermission string `json:"viewerPermission"`
		PullRequest      *struct {
			Number              int         `json:"number"`
			Title               string      `json:"title"`
			URL                 string      `json:"url"`
			State               string      `json:"state"`
			IsDraft             bool        `json:"isDraft"`
			Body                string      `json:"body"`
			BodyHTML            string      `json:"bodyHTML"`
			CreatedAt           string      `json:"createdAt"`
			UpdatedAt           string      `json:"updatedAt"`
			Author              *authorNode `json:"author"`
			HeadRefName         string      `json:"headRefName"`
			HeadRefOid          string      `json:"headRefOid"`
			BaseRefName         string      `json:"baseRefName"`
			BaseRefOid          string      `json:"baseRefOid"`
			IsCrossRepository   bool        `json:"isCrossRepository"`
			MaintainerCanModify bool        `json:"maintainerCanModify"`
			HeadRepository      *struct {
				NameWithOwner string `json:"nameWithOwner"`
			} `json:"headRepository"`
			Additions      int               `json:"additions"`
			Deletions      int               `json:"deletions"`
			ChangedFiles   int               `json:"changedFiles"`
			Mergeable      string            `json:"mergeable"`
			ReviewDecision string            `json:"reviewDecision"`
			Comments       commentConnection `json:"comments"`
			Reviews        struct {
				Nodes []struct {
					Author      *authorNode `json:"author"`
					State       string      `json:"state"`
					Body        string      `json:"body"`
					BodyHTML    string      `json:"bodyHTML"`
					SubmittedAt string      `json:"submittedAt"`
					URL         string      `json:"url"`
				} `json:"nodes"`
			} `json:"reviews"`
			ReviewThreads struct {
				Nodes []struct {
					IsResolved bool              `json:"isResolved"`
					IsOutdated bool              `json:"isOutdated"`
					Path       string            `json:"path"`
					Line       int               `json:"line"`
					Comments   commentConnection `json:"comments"`
				} `json:"nodes"`
			} `json:"reviewThreads"`
			Files struct {
				TotalCount int `json:"totalCount"`
				Nodes      []struct {
					Path       string `json:"path"`
					Additions  int    `json:"additions"`
					Deletions  int    `json:"deletions"`
					ChangeType string `json:"changeType"`
				} `json:"nodes"`
			} `json:"files"`
			Commits struct {
				Nodes []struct {
					Commit struct {
						OID               string `json:"oid"`
						StatusCheckRollup *struct {
							State    string `json:"state"`
							Contexts struct {
								Nodes []struct {
									TypeName   string `json:"__typename"`
									Name       string `json:"name"`
									Status     string `json:"status"`
									Conclusion string `json:"conclusion"`
									DetailsURL string `json:"detailsUrl"`
									Context    string `json:"context"`
									State      string `json:"state"`
									TargetURL  string `json:"targetUrl"`
								} `json:"nodes"`
							} `json:"contexts"`
						} `json:"statusCheckRollup"`
					} `json:"commit"`
				} `json:"nodes"`
			} `json:"commits"`
		} `json:"pullRequest"`
	} `json:"repository"`
}

type issueDetailResponse struct {
	Repository *struct {
		ViewerPermission string `json:"viewerPermission"`
		Issue            *struct {
			Number    int               `json:"number"`
			Title     string            `json:"title"`
			URL       string            `json:"url"`
			State     string            `json:"state"`
			Body      string            `json:"body"`
			BodyHTML  string            `json:"bodyHTML"`
			CreatedAt string            `json:"createdAt"`
			UpdatedAt string            `json:"updatedAt"`
			Author    *authorNode       `json:"author"`
			Comments  commentConnection `json:"comments"`
		} `json:"issue"`
	} `json:"repository"`
}

var (
	viewerMu     sync.Mutex
	cachedViewer *Viewer
	viewerAt     time.Time
)

// GetViewer 返回当前 gh 登录用户，结果缓存 10 分钟。
func GetViewer(ctx context.Context) (Viewer, error) {
	viewerMu.Lock()
	if cachedViewer != nil && time.Since(viewerAt) < 10*time.Minute {
		viewer := *cachedViewer
		viewerMu.Unlock()
		return viewer, nil
	}
	viewerMu.Unlock()

	client, err := api.DefaultGraphQLClient()
	if err != nil {
		return Viewer{}, fmt.Errorf("GitHub CLI 未登录，请在终端运行 gh auth login: %w", err)
	}
	var response struct {
		Viewer Viewer `json:"viewer"`
	}
	if err := client.DoWithContext(ctx, viewerQuery, nil, &response); err != nil {
		return Viewer{}, err
	}
	viewerMu.Lock()
	cachedViewer = &response.Viewer
	viewerAt = time.Now()
	viewerMu.Unlock()
	return response.Viewer, nil
}

// FetchDetail 拉取 PR 或 Issue 的正文、评论、评审、文件和检查状态。
func FetchDetail(ctx context.Context, fullName, kind string, number int) (Detail, error) {
	owner, name, err := config.SplitRepo(fullName)
	if err != nil {
		return Detail{}, err
	}
	client, err := api.DefaultGraphQLClient()
	if err != nil {
		return Detail{}, fmt.Errorf("初始化 GitHub 客户端失败，请先运行 gh auth login: %w", err)
	}
	variables := map[string]any{"owner": owner, "name": name, "number": number}
	detail := Detail{
		Repo:      fullName,
		Number:    number,
		Kind:      kind,
		Comments:  []Comment{},
		Reviews:   []Review{},
		Files:     []ChangedFile{},
		Checks:    []Check{},
		FetchedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if kind == "issue" {
		var response issueDetailResponse
		if err := client.DoWithContext(ctx, issueDetailQuery, variables, &response); err != nil {
			return Detail{}, err
		}
		if response.Repository == nil || response.Repository.Issue == nil {
			return Detail{}, fmt.Errorf("找不到 %s#%d", fullName, number)
		}
		issue := response.Repository.Issue
		detail.ViewerPermission = response.Repository.ViewerPermission
		detail.Title, detail.URL, detail.State = issue.Title, issue.URL, strings.ToLower(issue.State)
		detail.Body, detail.BodyHTML = issue.Body, issue.BodyHTML
		detail.CreatedAt, detail.UpdatedAt = issue.CreatedAt, issue.UpdatedAt
		detail.Author = mapPerson(issue.Author)
		detail.CommentCount = issue.Comments.TotalCount
		detail.Comments = mapComments(issue.Comments.Nodes)
		return detail, nil
	}

	var response pullDetailResponse
	if err := client.DoWithContext(ctx, pullDetailQuery, variables, &response); err != nil {
		return Detail{}, err
	}
	if response.Repository == nil || response.Repository.PullRequest == nil {
		return Detail{}, fmt.Errorf("找不到 %s#%d", fullName, number)
	}
	pr := response.Repository.PullRequest
	detail.ViewerPermission = response.Repository.ViewerPermission
	detail.Title, detail.URL, detail.State, detail.IsDraft = pr.Title, pr.URL, strings.ToLower(pr.State), pr.IsDraft
	detail.Body, detail.BodyHTML = pr.Body, pr.BodyHTML
	detail.CreatedAt, detail.UpdatedAt = pr.CreatedAt, pr.UpdatedAt
	detail.Author = mapPerson(pr.Author)
	detail.HeadRef, detail.HeadOID, detail.BaseRef, detail.BaseOID = pr.HeadRefName, pr.HeadRefOid, pr.BaseRefName, pr.BaseRefOid
	if pr.HeadRepository != nil {
		detail.HeadRepo = pr.HeadRepository.NameWithOwner
	}
	detail.IsCrossRepository, detail.MaintainerCanModify = pr.IsCrossRepository, pr.MaintainerCanModify
	detail.Additions, detail.Deletions, detail.ChangedFiles = pr.Additions, pr.Deletions, pr.ChangedFiles
	detail.Mergeable, detail.ReviewDecision = pr.Mergeable, pr.ReviewDecision
	detail.CommentCount = pr.Comments.TotalCount
	detail.Comments = mapComments(pr.Comments.Nodes)
	for _, review := range pr.Reviews.Nodes {
		detail.Reviews = append(detail.Reviews, Review{
			Author: mapPerson(review.Author), State: review.State, Body: review.Body,
			BodyHTML: review.BodyHTML, SubmittedAt: review.SubmittedAt, URL: review.URL,
		})
	}
	for _, thread := range pr.ReviewThreads.Nodes {
		detail.ReviewThreads = append(detail.ReviewThreads, ReviewThread{
			IsResolved: thread.IsResolved, IsOutdated: thread.IsOutdated, Path: thread.Path,
			Line: thread.Line, Comments: mapComments(thread.Comments.Nodes),
		})
	}
	if detail.ReviewThreads == nil {
		detail.ReviewThreads = []ReviewThread{}
	}
	detail.FileCount = pr.Files.TotalCount
	for _, file := range pr.Files.Nodes {
		detail.Files = append(detail.Files, ChangedFile{
			Path: file.Path, Additions: file.Additions, Deletions: file.Deletions, ChangeType: file.ChangeType,
		})
	}
	if len(pr.Commits.Nodes) > 0 && pr.Commits.Nodes[0].Commit.StatusCheckRollup != nil {
		rollup := pr.Commits.Nodes[0].Commit.StatusCheckRollup
		detail.CheckState = rollup.State
		for _, check := range rollup.Contexts.Nodes {
			if check.TypeName == "StatusContext" {
				detail.Checks = append(detail.Checks, Check{
					Name: check.Context, Status: "COMPLETED", Conclusion: check.State, URL: check.TargetURL,
				})
				continue
			}
			detail.Checks = append(detail.Checks, Check{
				Name: check.Name, Status: check.Status, Conclusion: check.Conclusion, URL: check.DetailsURL,
			})
		}
	}
	return detail, nil
}

func mapPerson(author *authorNode) Person {
	if author == nil {
		return Person{Login: "ghost"}
	}
	return Person{Login: author.Login, AvatarURL: author.AvatarURL}
}

func mapComments(nodes []commentNode) []Comment {
	comments := make([]Comment, 0, len(nodes))
	for _, node := range nodes {
		comments = append(comments, Comment{
			Author: mapPerson(node.Author), Body: node.Body, BodyHTML: node.BodyHTML,
			CreatedAt: node.CreatedAt, URL: node.URL,
		})
	}
	return comments
}

// PostComment 在 Issue 或 PR 下发表评论，返回评论链接。
func PostComment(ctx context.Context, fullName string, number int, body string) (string, error) {
	owner, name, err := config.SplitRepo(fullName)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(body) == "" {
		return "", errors.New("评论内容不能为空")
	}
	var response struct {
		HTMLURL string `json:"html_url"`
	}
	path := fmt.Sprintf("repos/%s/%s/issues/%d/comments", url.PathEscape(owner), url.PathEscape(name), number)
	if err := restPost(ctx, path, map[string]any{"body": body}, &response); err != nil {
		return "", err
	}
	return response.HTMLURL, nil
}

// PostReview 提交 PR 评审。event 取 COMMENT / APPROVE / REQUEST_CHANGES。
func PostReview(ctx context.Context, fullName string, number int, body, event, commitID string) (string, error) {
	owner, name, err := config.SplitRepo(fullName)
	if err != nil {
		return "", err
	}
	switch event {
	case "COMMENT", "APPROVE", "REQUEST_CHANGES":
	default:
		return "", fmt.Errorf("不支持的评审类型: %q", event)
	}
	if event != "APPROVE" && strings.TrimSpace(body) == "" {
		return "", errors.New("评审内容不能为空")
	}
	payload := map[string]any{"body": body, "event": event}
	if commitID != "" {
		payload["commit_id"] = commitID
	}
	var response struct {
		HTMLURL string `json:"html_url"`
	}
	path := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", url.PathEscape(owner), url.PathEscape(name), number)
	if err := restPost(ctx, path, payload, &response); err != nil {
		return "", err
	}
	return response.HTMLURL, nil
}

// CreatePullRequest 创建 Pull Request，返回链接。
func CreatePullRequest(ctx context.Context, fullName, head, base, title, body string, draft bool) (string, error) {
	owner, name, err := config.SplitRepo(fullName)
	if err != nil {
		return "", err
	}
	var response struct {
		HTMLURL string `json:"html_url"`
	}
	path := fmt.Sprintf("repos/%s/%s/pulls", url.PathEscape(owner), url.PathEscape(name))
	payload := map[string]any{"title": title, "head": head, "base": base, "body": body, "draft": draft}
	if err := restPost(ctx, path, payload, &response); err != nil {
		return "", err
	}
	return response.HTMLURL, nil
}

func restPost(ctx context.Context, path string, payload any, response any) error {
	client, err := api.DefaultRESTClient()
	if err != nil {
		return fmt.Errorf("初始化 GitHub 客户端失败，请先运行 gh auth login: %w", err)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return client.DoWithContext(ctx, "POST", path, bytes.NewReader(data), response)
}
