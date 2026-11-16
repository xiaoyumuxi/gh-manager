package githubsync

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/xiaoyumuxi/gh-manager/internal/config"
	"github.com/xiaoyumuxi/gh-manager/internal/store"
)

const issueOpenQuery = `
query($owner: String!, $name: String!, $first: Int!, $after: String) {
  repository(owner: $owner, name: $name) {
    issues(first: $first, after: $after, states: OPEN, orderBy: {field: UPDATED_AT, direction: DESC}) {
      pageInfo { hasNextPage endCursor }
      nodes {
        number
        title
        url
        state
        createdAt
        updatedAt
        closedAt
        author { login }
        body
        labels(first: 50) { nodes { name color } }
        assignees(first: 10) { nodes { login } }
        milestone { title }
        comments { totalCount }
      }
    }
  }
}`

const issueClosedQuery = `
query($owner: String!, $name: String!, $first: Int!, $after: String, $since: DateTime!) {
  repository(owner: $owner, name: $name) {
    issues(first: $first, after: $after, states: CLOSED, filterBy: {since: $since}, orderBy: {field: UPDATED_AT, direction: DESC}) {
      pageInfo { hasNextPage endCursor }
      nodes {
        number
        title
        url
        state
        createdAt
        updatedAt
        closedAt
        author { login }
        body
        labels(first: 50) { nodes { name color } }
        assignees(first: 10) { nodes { login } }
        milestone { title }
        comments { totalCount }
      }
    }
  }
}`

const pullOpenQuery = `
query($owner: String!, $name: String!, $first: Int!, $after: String) {
  repository(owner: $owner, name: $name) {
    pullRequests(first: $first, after: $after, states: OPEN, orderBy: {field: UPDATED_AT, direction: DESC}) {
      pageInfo { hasNextPage endCursor }
      nodes {
        number
        title
        url
        state
        isDraft
        createdAt
        updatedAt
        closedAt
        mergedAt
        author { login }
        body
        labels(first: 50) { nodes { name color } }
        assignees(first: 10) { nodes { login } }
        milestone { title }
        comments { totalCount }
        reviewDecision
        reviewRequests(first: 20) {
          nodes {
            requestedReviewer {
              __typename
              ... on User { login }
              ... on Team { name }
            }
          }
        }
        statusCheckRollup { state }
        mergeable
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
      }
    }
  }
}`

const pullClosedQuery = `
query($owner: String!, $name: String!, $first: Int!, $after: String) {
  repository(owner: $owner, name: $name) {
    pullRequests(first: $first, after: $after, states: [CLOSED, MERGED], orderBy: {field: UPDATED_AT, direction: DESC}) {
      pageInfo { hasNextPage endCursor }
      nodes {
        number
        title
        url
        state
        isDraft
        createdAt
        updatedAt
        closedAt
        mergedAt
        author { login }
        body
        labels(first: 50) { nodes { name color } }
        assignees(first: 10) { nodes { login } }
        milestone { title }
        comments { totalCount }
        reviewDecision
        reviewRequests(first: 20) {
          nodes {
            requestedReviewer {
              __typename
              ... on User { login }
              ... on Team { name }
            }
          }
        }
        statusCheckRollup { state }
        mergeable
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
      }
    }
  }
}`

const repositoryOwnerReposQuery = `
query($owner: String!, $first: Int!, $after: String) {
  repositoryOwner(login: $owner) {
    __typename
    repositories(first: $first, after: $after, orderBy: {field: UPDATED_AT, direction: DESC}) {
      pageInfo { hasNextPage endCursor }
      nodes { nameWithOwner isArchived }
    }
  }
}`

const repositoryDefaultHeadQuery = `
query($owner: String!, $name: String!) {
  repository(owner: $owner, name: $name) {
    defaultBranchRef {
      name
      target {
        __typename
        ... on Commit { oid messageHeadline committedDate }
      }
    }
  }
}`

const repositoryBranchHeadQuery = `
query($owner: String!, $name: String!, $qualifiedName: String!) {
  repository(owner: $owner, name: $name) {
    ref(qualifiedName: $qualifiedName) {
      name
      target {
        __typename
        ... on Commit { oid messageHeadline committedDate }
      }
    }
  }
}`

type Client struct {
	graphql *api.GraphQLClient
}

type RepoHead struct {
	Branch      string
	SHA         string
	Message     string
	CommittedAt time.Time
}

type pageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type actor struct {
	Login string `json:"login"`
}

type labelNode struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

type issueNode struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	State     string `json:"state"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	ClosedAt  string `json:"closedAt"`
	Author    *actor `json:"author"`
	Body      string `json:"body"`
	Labels    struct {
		Nodes []labelNode `json:"nodes"`
	} `json:"labels"`
	Assignees struct {
		Nodes []actor `json:"nodes"`
	} `json:"assignees"`
	Milestone *struct {
		Title string `json:"title"`
	} `json:"milestone"`
	Comments struct {
		TotalCount int `json:"totalCount"`
	} `json:"comments"`
}

type pullRequestNode struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	State     string `json:"state"`
	IsDraft   bool   `json:"isDraft"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	ClosedAt  string `json:"closedAt"`
	MergedAt  string `json:"mergedAt"`
	Author    *actor `json:"author"`
	Body      string `json:"body"`
	Labels    struct {
		Nodes []labelNode `json:"nodes"`
	} `json:"labels"`
	Assignees struct {
		Nodes []actor `json:"nodes"`
	} `json:"assignees"`
	Milestone *struct {
		Title string `json:"title"`
	} `json:"milestone"`
	Comments struct {
		TotalCount int `json:"totalCount"`
	} `json:"comments"`
	ReviewDecision string `json:"reviewDecision"`
	ReviewRequests struct {
		Nodes []struct {
			RequestedReviewer struct {
				TypeName string `json:"__typename"`
				Login    string `json:"login"`
				Name     string `json:"name"`
			} `json:"requestedReviewer"`
		} `json:"nodes"`
	} `json:"reviewRequests"`
	StatusCheckRollup *struct {
		State string `json:"state"`
	} `json:"statusCheckRollup"`
	Mergeable           string `json:"mergeable"`
	HeadRefName         string `json:"headRefName"`
	HeadRefOid          string `json:"headRefOid"`
	BaseRefName         string `json:"baseRefName"`
	BaseRefOid          string `json:"baseRefOid"`
	IsCrossRepository   bool   `json:"isCrossRepository"`
	MaintainerCanModify bool   `json:"maintainerCanModify"`
	HeadRepository      *struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"headRepository"`
	Additions    int `json:"additions"`
	Deletions    int `json:"deletions"`
	ChangedFiles int `json:"changedFiles"`
}

type issueConnection struct {
	PageInfo pageInfo    `json:"pageInfo"`
	Nodes    []issueNode `json:"nodes"`
}

type pullConnection struct {
	PageInfo pageInfo          `json:"pageInfo"`
	Nodes    []pullRequestNode `json:"nodes"`
}

type issueResponse struct {
	Repository *struct {
		Issues issueConnection `json:"issues"`
	} `json:"repository"`
}

type pullResponse struct {
	Repository *struct {
		PullRequests pullConnection `json:"pullRequests"`
	} `json:"repository"`
}

type repositoryConnection struct {
	PageInfo pageInfo `json:"pageInfo"`
	Nodes    []struct {
		NameWithOwner string `json:"nameWithOwner"`
		IsArchived    bool   `json:"isArchived"`
	} `json:"nodes"`
}

type ownerReposResponse struct {
	RepositoryOwner *struct {
		TypeName     string               `json:"__typename"`
		Repositories repositoryConnection `json:"repositories"`
	} `json:"repositoryOwner"`
}

type branchTarget struct {
	TypeName        string `json:"__typename"`
	OID             string `json:"oid"`
	MessageHeadline string `json:"messageHeadline"`
	CommittedDate   string `json:"committedDate"`
}

type branchRef struct {
	Name   string        `json:"name"`
	Target *branchTarget `json:"target"`
}

type repositoryHeadResponse struct {
	Repository *struct {
		DefaultBranchRef *branchRef `json:"defaultBranchRef"`
		Ref              *branchRef `json:"ref"`
	} `json:"repository"`
}

func NewClient() (*Client, error) {
	client, err := api.DefaultGraphQLClient()
	if err != nil {
		return nil, fmt.Errorf("初始化 GitHub 客户端失败，请先运行 gh auth login: %w", err)
	}
	return &Client{graphql: client}, nil
}

func (c *Client) listIssues(ctx context.Context, owner, name string, first int, closed bool, since time.Time) ([]store.GitHubItem, bool, error) {
	query := issueOpenQuery
	variables := map[string]any{
		"owner": owner,
		"name":  name,
		"first": first,
		"after": nil,
	}
	if closed {
		query = issueClosedQuery
		variables["since"] = since.UTC().Format(time.RFC3339)
	}

	items := make([]store.GitHubItem, 0)
	for {
		var response issueResponse
		if err := c.graphql.DoWithContext(ctx, query, variables, &response); err != nil {
			return items, false, err
		}
		if response.Repository == nil {
			return items, false, fmt.Errorf("仓库不存在或无权限访问: %s/%s", owner, name)
		}
		for _, node := range response.Repository.Issues.Nodes {
			items = append(items, mapIssue(owner, name, node))
		}
		if !response.Repository.Issues.PageInfo.HasNextPage {
			return items, true, nil
		}
		variables["after"] = response.Repository.Issues.PageInfo.EndCursor
	}
}

func (c *Client) listPullRequests(ctx context.Context, owner, name string, first int, closed bool, cutoff time.Time) ([]store.GitHubItem, bool, error) {
	query := pullOpenQuery
	if closed {
		query = pullClosedQuery
	}
	variables := map[string]any{
		"owner": owner,
		"name":  name,
		"first": first,
		"after": nil,
	}

	items := make([]store.GitHubItem, 0)
	for {
		var response pullResponse
		if err := c.graphql.DoWithContext(ctx, query, variables, &response); err != nil {
			return items, false, err
		}
		if response.Repository == nil {
			return items, false, fmt.Errorf("仓库不存在或无权限访问: %s/%s", owner, name)
		}
		reachedCutoff := false
		for _, node := range response.Repository.PullRequests.Nodes {
			updatedAt, _ := parseGitHubTime(node.UpdatedAt)
			if closed && !cutoff.IsZero() && !updatedAt.IsZero() && updatedAt.Before(cutoff) {
				reachedCutoff = true
				continue
			}
			items = append(items, mapPullRequest(owner, name, node))
		}
		if reachedCutoff || !response.Repository.PullRequests.PageInfo.HasNextPage {
			return items, true, nil
		}
		variables["after"] = response.Repository.PullRequests.PageInfo.EndCursor
	}
}

func GetRepositoryHead(ctx context.Context, fullName, branch string) (RepoHead, error) {
	owner, name, err := config.SplitRepo(fullName)
	if err != nil {
		return RepoHead{}, err
	}
	client, err := NewClient()
	if err != nil {
		return RepoHead{}, err
	}
	return client.repositoryHead(ctx, owner, name, config.NormalizeBranchName(branch))
}

func (c *Client) repositoryHead(ctx context.Context, owner, name, branch string) (RepoHead, error) {
	query := repositoryDefaultHeadQuery
	variables := map[string]any{"owner": owner, "name": name}
	if branch != "" {
		query = repositoryBranchHeadQuery
		variables["qualifiedName"] = "refs/heads/" + branch
	}

	var response repositoryHeadResponse
	if err := c.graphql.DoWithContext(ctx, query, variables, &response); err != nil {
		return RepoHead{}, err
	}
	if response.Repository == nil {
		return RepoHead{}, fmt.Errorf("仓库不存在或无权限访问: %s/%s", owner, name)
	}

	ref := response.Repository.DefaultBranchRef
	if branch != "" {
		ref = response.Repository.Ref
		if ref == nil {
			return RepoHead{}, fmt.Errorf("分支 %q 不存在: %s/%s", branch, owner, name)
		}
	} else if ref == nil {
		return RepoHead{}, fmt.Errorf("仓库没有默认分支（可能是空仓库）: %s/%s", owner, name)
	}
	if ref.Target == nil || strings.TrimSpace(ref.Target.OID) == "" {
		return RepoHead{}, fmt.Errorf("分支 %q 尚无提交: %s/%s", ref.Name, owner, name)
	}
	committedAt, err := parseGitHubTime(ref.Target.CommittedDate)
	if err != nil || committedAt.IsZero() {
		return RepoHead{}, fmt.Errorf("无法读取分支 %q 的最新提交时间: %s/%s", ref.Name, owner, name)
	}
	return RepoHead{
		Branch:      ref.Name,
		SHA:         ref.Target.OID,
		Message:     ref.Target.MessageHeadline,
		CommittedAt: committedAt.UTC(),
	}, nil
}

func ListRepositories(ctx context.Context, owner string) ([]string, error) {
	client, err := api.DefaultGraphQLClient()
	if err != nil {
		return nil, fmt.Errorf("初始化 GitHub 客户端失败，请先运行 gh auth login: %w", err)
	}

	const first = 100
	var after any
	repos := make([]string, 0)
	for {
		var response ownerReposResponse
		if err := client.DoWithContext(ctx, repositoryOwnerReposQuery, map[string]any{
			"owner": owner, "first": first, "after": after,
		}, &response); err != nil {
			return nil, err
		}
		if response.RepositoryOwner == nil {
			return nil, fmt.Errorf("找不到组织或用户: %s", owner)
		}
		repos, after = appendRepos(repos, response.RepositoryOwner.Repositories)
		if !response.RepositoryOwner.Repositories.PageInfo.HasNextPage {
			break
		}
		after = response.RepositoryOwner.Repositories.PageInfo.EndCursor
	}
	sort.Strings(repos)
	return repos, nil
}

func appendRepos(repos []string, connection repositoryConnection) ([]string, any) {
	for _, repo := range connection.Nodes {
		if !repo.IsArchived && repo.NameWithOwner != "" {
			repos = append(repos, repo.NameWithOwner)
		}
	}
	return repos, connection.PageInfo.EndCursor
}

func mapIssue(owner, name string, node issueNode) store.GitHubItem {
	createdAt, _ := parseGitHubTime(node.CreatedAt)
	updatedAt, _ := parseGitHubTime(node.UpdatedAt)
	closedAt := parseGitHubOptionalTime(node.ClosedAt)
	labels := make([]store.Label, 0, len(node.Labels.Nodes))
	for _, label := range node.Labels.Nodes {
		labels = append(labels, store.Label{Name: label.Name, Color: normalizeColor(label.Color)})
	}
	assignees := make([]string, 0, len(node.Assignees.Nodes))
	for _, assignee := range node.Assignees.Nodes {
		assignees = append(assignees, assignee.Login)
	}
	milestone := ""
	if node.Milestone != nil {
		milestone = node.Milestone.Title
	}
	author := ""
	if node.Author != nil {
		author = node.Author.Login
	}
	return store.GitHubItem{
		Repo:      owner + "/" + name,
		Number:    node.Number,
		Kind:      "issue",
		Title:     node.Title,
		URL:       node.URL,
		State:     node.State,
		Author:    author,
		Body:      node.Body,
		Labels:    labels,
		Assignees: assignees,
		Milestone: milestone,
		Comments:  node.Comments.TotalCount,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
		ClosedAt:  closedAt,
	}
}

func mapPullRequest(owner, name string, node pullRequestNode) store.GitHubItem {
	createdAt, _ := parseGitHubTime(node.CreatedAt)
	updatedAt, _ := parseGitHubTime(node.UpdatedAt)
	closedAt := parseGitHubOptionalTime(node.ClosedAt)
	mergedAt := parseGitHubOptionalTime(node.MergedAt)
	labels := make([]store.Label, 0, len(node.Labels.Nodes))
	for _, label := range node.Labels.Nodes {
		labels = append(labels, store.Label{Name: label.Name, Color: normalizeColor(label.Color)})
	}
	assignees := make([]string, 0, len(node.Assignees.Nodes))
	for _, assignee := range node.Assignees.Nodes {
		assignees = append(assignees, assignee.Login)
	}
	reviewers := make([]string, 0, len(node.ReviewRequests.Nodes))
	for _, request := range node.ReviewRequests.Nodes {
		if request.RequestedReviewer.Login != "" {
			reviewers = append(reviewers, request.RequestedReviewer.Login)
		} else if request.RequestedReviewer.Name != "" {
			reviewers = append(reviewers, request.RequestedReviewer.Name)
		}
	}
	milestone := ""
	if node.Milestone != nil {
		milestone = node.Milestone.Title
	}
	author := ""
	if node.Author != nil {
		author = node.Author.Login
	}
	ciStatus := ""
	if node.StatusCheckRollup != nil {
		ciStatus = node.StatusCheckRollup.State
	}
	headRepo := ""
	if node.HeadRepository != nil {
		headRepo = node.HeadRepository.NameWithOwner
	}
	pr := &store.PRMeta{
		HeadRef:             node.HeadRefName,
		HeadOID:             node.HeadRefOid,
		HeadRepo:            headRepo,
		BaseRef:             node.BaseRefName,
		BaseOID:             node.BaseRefOid,
		IsCrossRepository:   node.IsCrossRepository,
		MaintainerCanModify: node.MaintainerCanModify,
		Additions:           node.Additions,
		Deletions:           node.Deletions,
		ChangedFiles:        node.ChangedFiles,
	}
	return store.GitHubItem{
		Repo:           owner + "/" + name,
		Number:         node.Number,
		Kind:           "pr",
		Title:          node.Title,
		URL:            node.URL,
		State:          node.State,
		IsDraft:        node.IsDraft,
		Author:         author,
		Body:           node.Body,
		Labels:         labels,
		Assignees:      assignees,
		Reviewers:      reviewers,
		Milestone:      milestone,
		ReviewDecision: node.ReviewDecision,
		CIStatus:       ciStatus,
		Mergeable:      node.Mergeable,
		Comments:       node.Comments.TotalCount,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
		ClosedAt:       closedAt,
		MergedAt:       mergedAt,
		PR:             pr,
	}
}

func parseGitHubTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, value)
}

func parseGitHubOptionalTime(value string) *time.Time {
	t, err := parseGitHubTime(value)
	if err != nil || t.IsZero() {
		return nil
	}
	t = t.UTC()
	return &t
}

func normalizeColor(color string) string {
	if color == "" {
		return "#8C8C8C"
	}
	if color[0] != '#' {
		return "#" + color
	}
	return color
}
