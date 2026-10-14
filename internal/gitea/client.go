package gitea

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type User struct {
	ID       int64  `json:"id"`
	Login    string `json:"login"`
	FullName string `json:"full_name"`
}

type Repo struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	FullName      string    `json:"full_name"`
	Owner         User      `json:"owner"`
	Description   string    `json:"description"`
	Private       bool      `json:"private"`
	Archived      bool      `json:"archived"`
	DefaultBranch string    `json:"default_branch"`
	UpdatedAt     time.Time `json:"updated_at"`
	HTMLURL       string    `json:"html_url"`
	OpenPRs       int       `json:"open_pr_counter"`
}

type Ref struct {
	Ref  string `json:"ref"`
	SHA  string `json:"sha"`
	Repo *Repo  `json:"repo"`
}

type Label struct {
	Name string `json:"name"`
}

type Pull struct {
	Number       int64      `json:"number"`
	Title        string     `json:"title"`
	Body         string     `json:"body"`
	State        string     `json:"state"`
	Draft        bool       `json:"draft"`
	User         User       `json:"user"`
	HTMLURL      string     `json:"html_url"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	MergedAt     *time.Time `json:"merged_at"`
	Merged       bool       `json:"merged"`
	Mergeable    bool       `json:"mergeable"`
	Additions    int        `json:"additions"`
	Deletions    int        `json:"deletions"`
	ChangedFiles int        `json:"changed_files"`
	Labels       []Label    `json:"labels"`
	Head         Ref        `json:"head"`
	Base         Ref        `json:"base"`
}

type PullFile struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
	Status           string `json:"status"`
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
}

type Review struct {
	ID            int64     `json:"id"`
	User          User      `json:"user"`
	State         string    `json:"state"`
	Body          string    `json:"body"`
	CommitID      string    `json:"commit_id"`
	Stale         bool      `json:"stale"`
	Dismissed     bool      `json:"dismissed"`
	SubmittedAt   time.Time `json:"submitted_at"`
	CommentsCount int       `json:"comments_count"`
	HTMLURL       string    `json:"html_url"`
}

type ReviewComment struct {
	ID               int64     `json:"id"`
	ReviewID         int64     `json:"pull_request_review_id"`
	Path             string    `json:"path"`
	Body             string    `json:"body"`
	User             User      `json:"user"`
	Resolver         *User     `json:"resolver"`
	CommitID         string    `json:"commit_id"`
	OriginalCommitID string    `json:"original_commit_id"`
	Position         uint64    `json:"position"`
	OriginalPosition uint64    `json:"original_position"`
	DiffHunk         string    `json:"diff_hunk"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	HTMLURL          string    `json:"html_url"`
}

/*
 * Gitea stores an old-side line as a negative number and then casts it
 * to uint64 for the API, so anything huge is really "-n on the old side".
 */
func (c ReviewComment) Line() (int64, string) {
	if c.Position > 1<<62 {
		return int64(^c.Position + 1), "old"
	}
	return int64(c.Position), "new"
}

type IssueComment struct {
	ID        int64     `json:"id"`
	Body      string    `json:"body"`
	User      User      `json:"user"`
	CreatedAt time.Time `json:"created_at"`
	HTMLURL   string    `json:"html_url"`
}

type Entry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

type Commit struct {
	SHA     string    `json:"sha"`
	HTMLURL string    `json:"html_url"`
	Created time.Time `json:"created"`
	Commit  struct {
		Message string `json:"message"`
		Author  struct {
			Name string    `json:"name"`
			Date time.Time `json:"date"`
		} `json:"author"`
	} `json:"commit"`
}

type CommitStatus struct {
	Context     string `json:"context"`
	Status      string `json:"status"`
	Description string `json:"description"`
	TargetURL   string `json:"target_url"`
}

type CombinedStatus struct {
	State    string         `json:"state"`
	SHA      string         `json:"sha"`
	Statuses []CommitStatus `json:"statuses"`
}

type NewComment struct {
	Path        string `json:"path"`
	Body        string `json:"body"`
	NewPosition int64  `json:"new_position,omitempty"`
	OldPosition int64  `json:"old_position,omitempty"`
}

type NewReview struct {
	Body     string       `json:"body"`
	Event    string       `json:"event"`
	CommitID string       `json:"commit_id,omitempty"`
	Comments []NewComment `json:"comments"`
}

type Error struct {
	Status int
	Method string
	Path   string
	Body   string
}

func (e *Error) Error() string {
	b := e.Body
	if len(b) > 400 {
		b = b[:400] + "..."
	}
	return fmt.Sprintf("gitea %s %s: %d %s", e.Method, e.Path, e.Status, b)
}

const pageSize = 50

type Client struct {
	BaseURL string
	token   string
	http    *http.Client
}

func New(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

var origin = regexp.MustCompile(`^https?://[^/]+`)

/*
 * Gitea builds html_url from its ROOT_URL, which is whatever the admin
 * typed into app.ini and is not necessarily reachable from here.
 */
func (c *Client) LocalURL(u string) string {
	if u == "" {
		return ""
	}
	return origin.ReplaceAllString(u, c.BaseURL)
}

func escapePath(p string) string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, in, out any) error {
	u := c.BaseURL + "/api/v1" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}

	var body io.Reader
	if in != nil {
		buf, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+c.token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	_, text := out.(*string)
	if text {
		req.Header.Set("Accept", "text/plain")
	} else {
		req.Header.Set("Accept", "application/json")
	}

	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("gitea %s %s: %w", method, path, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode/100 != 2 {
		return &Error{res.StatusCode, method, path, string(data)}
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if text {
		*out.(*string) = string(data)
		return nil
	}
	return json.Unmarshal(data, out)
}

func all[T any](ctx context.Context, c *Client, path string, q url.Values) ([]T, error) {
	if q == nil {
		q = url.Values{}
	}
	var out []T
	for page := 1; ; page++ {
		q.Set("limit", fmt.Sprint(pageSize))
		q.Set("page", fmt.Sprint(page))
		var batch []T
		if err := c.do(ctx, "GET", path, q, nil, &batch); err != nil {
			return nil, err
		}
		out = append(out, batch...)
		if len(batch) < pageSize {
			return out, nil
		}
	}
}

func (c *Client) Me(ctx context.Context) (User, error) {
	var u User
	return u, c.do(ctx, "GET", "/user", nil, nil, &u)
}

func (c *Client) Version(ctx context.Context) (string, error) {
	var v struct {
		Version string `json:"version"`
	}
	return v.Version, c.do(ctx, "GET", "/version", nil, nil, &v)
}

func (c *Client) Repos(ctx context.Context) ([]Repo, error) {
	return all[Repo](ctx, c, "/user/repos", nil)
}

func (c *Client) Pulls(ctx context.Context, owner, repo, state string) ([]Pull, error) {
	q := url.Values{"state": {state}, "sort": {"recentupdate"}}
	return all[Pull](ctx, c, fmt.Sprintf("/repos/%s/%s/pulls", owner, repo), q)
}

func (c *Client) Pull(ctx context.Context, owner, repo string, n int64) (Pull, error) {
	var p Pull
	return p, c.do(ctx, "GET", fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repo, n), nil, nil, &p)
}

func (c *Client) Diff(ctx context.Context, owner, repo string, n int64) (string, error) {
	var s string
	return s, c.do(ctx, "GET", fmt.Sprintf("/repos/%s/%s/pulls/%d.diff", owner, repo, n), nil, nil, &s)
}

func (c *Client) Files(ctx context.Context, owner, repo string, n int64) ([]PullFile, error) {
	return all[PullFile](ctx, c, fmt.Sprintf("/repos/%s/%s/pulls/%d/files", owner, repo, n), nil)
}

func (c *Client) Commits(ctx context.Context, owner, repo string, n int64) ([]Commit, error) {
	return all[Commit](ctx, c, fmt.Sprintf("/repos/%s/%s/pulls/%d/commits", owner, repo, n), nil)
}

func (c *Client) Status(ctx context.Context, owner, repo, sha string) (CombinedStatus, error) {
	var s CombinedStatus
	return s, c.do(ctx, "GET", fmt.Sprintf("/repos/%s/%s/commits/%s/status", owner, repo, sha), nil, nil, &s)
}

func (c *Client) Reviews(ctx context.Context, owner, repo string, n int64) ([]Review, error) {
	return all[Review](ctx, c, fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews", owner, repo, n), nil)
}

func (c *Client) ReviewComments(ctx context.Context, owner, repo string, n, review int64) ([]ReviewComment, error) {
	var out []ReviewComment
	return out, c.do(ctx, "GET", fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews/%d/comments", owner, repo, n, review), nil, nil, &out)
}

func (c *Client) IssueComments(ctx context.Context, owner, repo string, n int64) ([]IssueComment, error) {
	return all[IssueComment](ctx, c, fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repo, n), nil)
}

func (c *Client) Raw(ctx context.Context, owner, repo, path, ref string) (string, error) {
	var s string
	return s, c.do(ctx, "GET", fmt.Sprintf("/repos/%s/%s/raw/%s", owner, repo, escapePath(path)), url.Values{"ref": {ref}}, nil, &s)
}

func (c *Client) Ls(ctx context.Context, owner, repo, path, ref string) ([]Entry, error) {
	p := fmt.Sprintf("/repos/%s/%s/contents", owner, repo)
	if strings.Trim(path, "/") != "" {
		p += "/" + escapePath(path)
	}
	/* a file path returns one object, a directory returns an array */
	var raw json.RawMessage
	if err := c.do(ctx, "GET", p, url.Values{"ref": {ref}}, nil, &raw); err != nil {
		return nil, err
	}
	var list []Entry
	if json.Unmarshal(raw, &list) == nil {
		return list, nil
	}
	var one Entry
	if err := json.Unmarshal(raw, &one); err != nil {
		return nil, err
	}
	return []Entry{one}, nil
}

func (c *Client) CreateReview(ctx context.Context, owner, repo string, n int64, r NewReview) (Review, error) {
	if r.Comments == nil {
		r.Comments = []NewComment{}
	}
	var out Review
	return out, c.do(ctx, "POST", fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews", owner, repo, n), nil, r, &out)
}

func (c *Client) Comment(ctx context.Context, owner, repo string, n int64, body string) (IssueComment, error) {
	var out IssueComment
	return out, c.do(ctx, "POST", fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repo, n), nil, map[string]string{"body": body}, &out)
}
