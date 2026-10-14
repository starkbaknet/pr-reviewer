package review

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"prr/internal/store"
)

type CommentStatus struct {
	ID        int64     `json:"id"`
	ReviewID  int64     `json:"review_id"`
	Path      string    `json:"path"`
	Line      int64     `json:"line"`
	Side      string    `json:"side"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	Resolved  bool      `json:"resolved"`
	Resolver  string    `json:"resolver,omitempty"`
	Outdated  bool      `json:"outdated"`
	CreatedAt time.Time `json:"created_at"`
	URL       string    `json:"url"`
}

type ReviewStatus struct {
	ID          int64     `json:"id"`
	Author      string    `json:"author"`
	State       string    `json:"state"`
	Stale       bool      `json:"stale"`
	Dismissed   bool      `json:"dismissed"`
	SubmittedAt time.Time `json:"submitted_at"`
	Body        string    `json:"body"`
	URL         string    `json:"url"`
	Comments    int       `json:"comments"`
}

type CommitInfo struct {
	SHA     string    `json:"sha"`
	Message string    `json:"message"`
	Author  string    `json:"author"`
	Date    time.Time `json:"date"`
	URL     string    `json:"url"`
}

type IssueCommentInfo struct {
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	URL       string    `json:"url"`
}

type Check struct {
	Context     string `json:"context"`
	Status      string `json:"status"`
	Description string `json:"description"`
	URL         string `json:"url"`
}

type Status struct {
	Owner         string             `json:"owner"`
	Repo          string             `json:"repo"`
	Number        int64              `json:"number"`
	Title         string             `json:"title"`
	State         string             `json:"state"`
	Draft         bool               `json:"draft"`
	Mergeable     bool               `json:"mergeable"`
	HeadSHA       string             `json:"head_sha"`
	HeadRef       string             `json:"head_ref"`
	BaseRef       string             `json:"base_ref"`
	URL           string             `json:"url"`
	ReviewedSHA   string             `json:"reviewed_sha,omitempty"`
	NewCommits    []CommitInfo       `json:"new_commits"`
	CI            string             `json:"ci"`
	Checks        []Check            `json:"checks"`
	Reviews       []ReviewStatus     `json:"reviews"`
	Comments      []CommentStatus    `json:"comments"`
	IssueComments []IssueCommentInfo `json:"issue_comments"`
	Totals        struct {
		Comments int `json:"comments"`
		Resolved int `json:"resolved"`
		Open     int `json:"open"`
		Outdated int `json:"outdated"`
	} `json:"totals"`
	FetchedAt time.Time `json:"fetched_at"`
}

func (s *Service) Status(ctx context.Context, owner, repo string, n int64) (*Status, error) {
	pull, err := s.g.Pull(ctx, owner, repo, n)
	if err != nil {
		return nil, err
	}
	st := &Status{Owner: owner, Repo: repo, Number: n, Title: pull.Title, Draft: pull.Draft, Mergeable: pull.Mergeable,
		HeadSHA: pull.Head.SHA, HeadRef: pull.Head.Ref, BaseRef: pull.Base.Ref, URL: s.g.LocalURL(pull.HTMLURL), FetchedAt: time.Now(),
		NewCommits: []CommitInfo{}, Checks: []Check{}, Reviews: []ReviewStatus{}, Comments: []CommentStatus{}, IssueComments: []IssueCommentInfo{}}
	st.State = pull.State
	if pull.Merged {
		st.State = "merged"
	}

	/*
	 * "Since the review" means since the last review we posted, or
	 * failing that the newest review anyone posted with comments.
	 */
	var since time.Time
	if sess, _ := s.store.Get(store.ID(owner, repo, n)); sess != nil {
		for _, p := range sess.Posted {
			if p.Type == "review" && p.At.After(since) {
				since = p.At
				st.ReviewedSHA = p.HeadSHA
			}
		}
	}

	reviews, err := s.g.Reviews(ctx, owner, repo, n)
	if err != nil {
		return nil, err
	}
	for _, r := range reviews {
		if r.State == "" || r.State == "PENDING" {
			continue
		}
		st.Reviews = append(st.Reviews, ReviewStatus{ID: r.ID, Author: r.User.Login, State: r.State, Stale: r.Stale, Dismissed: r.Dismissed,
			SubmittedAt: r.SubmittedAt, Body: r.Body, URL: s.g.LocalURL(r.HTMLURL), Comments: r.CommentsCount})
		if since.IsZero() && r.SubmittedAt.After(since) && r.CommentsCount > 0 {
			since = r.SubmittedAt
			if st.ReviewedSHA == "" {
				st.ReviewedSHA = r.CommitID
			}
		}
		if r.CommentsCount == 0 {
			continue
		}
		cs, err := s.g.ReviewComments(ctx, owner, repo, n, r.ID)
		if err != nil {
			return nil, err
		}
		for _, c := range cs {
			line, side := c.Line()
			cst := CommentStatus{ID: c.ID, ReviewID: r.ID, Path: c.Path, Line: line, Side: side, Author: c.User.Login, Body: c.Body,
				Resolved: c.Resolver != nil, CreatedAt: c.CreatedAt, URL: s.g.LocalURL(c.HTMLURL)}
			if c.Resolver != nil {
				cst.Resolver = c.Resolver.Login
			}
			st.Comments = append(st.Comments, cst)
		}
	}

	commits, err := s.g.Commits(ctx, owner, repo, n)
	if err == nil {
		for _, c := range commits {
			when := c.Commit.Author.Date
			if when.IsZero() {
				when = c.Created
			}
			if !since.IsZero() && when.After(since) {
				msg := strings.SplitN(c.Commit.Message, "\n", 2)[0]
				st.NewCommits = append(st.NewCommits, CommitInfo{SHA: c.SHA, Message: msg, Author: c.Commit.Author.Name, Date: when, URL: s.g.LocalURL(c.HTMLURL)})
			}
		}
		sort.Slice(st.NewCommits, func(i, j int) bool { return st.NewCommits[i].Date.After(st.NewCommits[j].Date) })
	}
	for i := range st.Comments {
		for _, nc := range st.NewCommits {
			if nc.Date.After(st.Comments[i].CreatedAt) {
				st.Comments[i].Outdated = true
				break
			}
		}
	}

	if cs, err := s.g.Status(ctx, owner, repo, pull.Head.SHA); err == nil {
		st.CI = or(cs.State, "none")
		for _, c := range cs.Statuses {
			st.Checks = append(st.Checks, Check{Context: c.Context, Status: c.Status, Description: c.Description, URL: c.TargetURL})
		}
	} else {
		st.CI = "none"
	}

	if ics, err := s.g.IssueComments(ctx, owner, repo, n); err == nil {
		for _, c := range ics {
			st.IssueComments = append(st.IssueComments, IssueCommentInfo{Author: c.User.Login, Body: c.Body, CreatedAt: c.CreatedAt, URL: s.g.LocalURL(c.HTMLURL)})
		}
	}

	sort.Slice(st.Comments, func(i, j int) bool {
		if st.Comments[i].Path != st.Comments[j].Path {
			return st.Comments[i].Path < st.Comments[j].Path
		}
		return st.Comments[i].Line < st.Comments[j].Line
	})
	for _, c := range st.Comments {
		st.Totals.Comments++
		if c.Resolved {
			st.Totals.Resolved++
		} else {
			st.Totals.Open++
		}
		if c.Outdated {
			st.Totals.Outdated++
		}
	}
	return st, nil
}

func (st *Status) Render() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s/%s#%d: %s\nState: %s", st.Owner, st.Repo, st.Number, st.Title, st.State)
	if st.Draft {
		sb.WriteString(" (draft)")
	}
	fmt.Fprintf(&sb, ", mergeable: %v, head %s, CI: %s\n", st.Mergeable, clip(st.HeadSHA, 10), st.CI)
	for _, c := range st.Checks {
		fmt.Fprintf(&sb, "  - %s: %s %s\n", c.Context, c.Status, c.Description)
	}
	fmt.Fprintf(&sb, "\n## Inline comments: %d total, %d resolved, %d open, %d outdated by newer commits\n", st.Totals.Comments, st.Totals.Resolved, st.Totals.Open, st.Totals.Outdated)
	for _, c := range st.Comments {
		state := "OPEN"
		if c.Resolved {
			state = "resolved by " + c.Resolver
		}
		if c.Outdated {
			state += ", commits since"
		}
		fmt.Fprintf(&sb, "- [%s] %s:%s:%d (%s): %s\n", state, c.Path, c.Side, c.Line, c.Author, clip(strings.ReplaceAll(c.Body, "\n", " "), 200))
	}
	if len(st.NewCommits) > 0 {
		fmt.Fprintf(&sb, "\n## %d commits since the review\n", len(st.NewCommits))
		for _, c := range st.NewCommits {
			fmt.Fprintf(&sb, "- %s %s (%s, %s)\n", clip(c.SHA, 10), c.Message, c.Author, c.Date.Format("2006-01-02 15:04"))
		}
	} else if st.ReviewedSHA != "" {
		sb.WriteString("\nNo commits since the review.\n")
	}
	if len(st.Reviews) > 0 {
		sb.WriteString("\n## Reviews\n")
		for _, r := range st.Reviews {
			flags := ""
			if r.Stale {
				flags += " stale"
			}
			if r.Dismissed {
				flags += " dismissed"
			}
			fmt.Fprintf(&sb, "- #%d %s by %s (%s)%s\n", r.ID, r.State, r.Author, r.SubmittedAt.Format("2006-01-02 15:04"), flags)
		}
	}
	if len(st.IssueComments) > 0 {
		sb.WriteString("\n## Conversation\n")
		for _, c := range st.IssueComments {
			fmt.Fprintf(&sb, "- %s (%s): %s\n", c.Author, c.CreatedAt.Format("2006-01-02 15:04"), clip(strings.ReplaceAll(c.Body, "\n", " "), 300))
		}
	}
	return sb.String()
}
