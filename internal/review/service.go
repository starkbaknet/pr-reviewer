package review

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"prr/internal/config"
	"prr/internal/diff"
	"prr/internal/gitea"
	"prr/internal/store"
)

const readLimit = 150 * 1024

const Instructions = `prr: review Gitea pull requests together with the human.

1. list_repos / list_pulls to find the PR, or get_pull directly when the human names owner/repo#N.
2. get_pull returns metadata, the changed files and the annotated diff when it fits; otherwise get_diff per file.
   Diff lines have two number columns, OLD and NEW, then +/-/space. Cite NEW for added and context lines, OLD for removed lines.
3. Read callers, types, tests and config with read_file / list_dir whenever the diff alone cannot prove something. Do not guess at code you could read.
4. get_comments before posting, so nothing already raised is repeated.
5. Findings go to the human first: severity (blocker|major|minor|nit), path:line, the concrete failure, the fix. Record each with note(kind="finding") and finish with note(kind="summary"); the dashboard shows them.
6. Post only when asked. post_review takes structured comments: issue (the problem in its simplest form), fix (copy-pasteable code; replace_line=true when it replaces exactly the commented line), why (why we do it this way), why_not (the alternative and why not). Keep every field short.
7. review_status shows what the author resolved and pushed since; use it for follow-ups.`

type pr struct {
	pull   gitea.Pull
	files  []gitea.PullFile
	diffs  []diff.File
	byPath map[string]*diff.File
	at     time.Time
}

type Service struct {
	cfg   config.Config
	g     *gitea.Client
	store *store.Store
	mu    sync.Mutex
	cache map[string]*pr
}

func New(cfg config.Config, g *gitea.Client, st *store.Store) *Service {
	return &Service{cfg: cfg, g: g, store: st, cache: map[string]*pr{}}
}

func splitRepo(s string) (string, string, error) {
	owner, name, ok := strings.Cut(strings.TrimSpace(s), "/")
	if !ok || owner == "" || name == "" {
		return "", "", fmt.Errorf("repo must be owner/name, got %q", s)
	}
	return owner, name, nil
}

func (s *Service) load(ctx context.Context, owner, repo string, n int64, force bool) (*pr, error) {
	key := store.ID(owner, repo, n)

	s.mu.Lock()
	p, ok := s.cache[key]
	s.mu.Unlock()
	if ok && !force && time.Since(p.at) < 2*time.Minute {
		return p, nil
	}

	pull, err := s.g.Pull(ctx, owner, repo, n)
	if err != nil {
		return nil, err
	}
	if ok && !force && pull.Head.SHA == p.pull.Head.SHA {
		p.at = time.Now()
		return p, nil
	}
	files, err := s.g.Files(ctx, owner, repo, n)
	if err != nil {
		return nil, err
	}
	raw, err := s.g.Diff(ctx, owner, repo, n)
	if err != nil {
		return nil, err
	}

	p = &pr{pull: pull, files: files, diffs: diff.Parse(raw), byPath: map[string]*diff.File{}, at: time.Now()}
	for i := range p.diffs {
		p.byPath[p.diffs[i].Path] = &p.diffs[i]
	}
	s.mu.Lock()
	s.cache[key] = p
	s.mu.Unlock()
	return p, nil
}

func (s *Service) log(owner, repo string, n int64, p *pr, ev store.Event) {
	ev.At = time.Now()
	create := func() *store.Session {
		return &store.Session{
			Owner: owner, Repo: repo, Number: n,
			Title: p.pull.Title, Author: p.pull.User.Login,
			HeadRef: p.pull.Head.Ref, BaseRef: p.pull.Base.Ref, HeadSHA: p.pull.Head.SHA,
			URL: s.g.LocalURL(p.pull.HTMLURL),
		}
	}
	s.store.Update(store.ID(owner, repo, n), create, func(sess *store.Session) {
		sess.Title = p.pull.Title
		sess.HeadSHA = p.pull.Head.SHA
		sess.Events = append(sess.Events, ev)
	})
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func or(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

func fail(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}
}

type ReposIn struct {
	Query string `json:"query,omitempty" jsonschema:"substring filter on owner/name or description"`
}

type PullsIn struct {
	Repo  string `json:"repo" jsonschema:"owner/name"`
	State string `json:"state,omitempty" jsonschema:"open (default), closed or all"`
}

type PRIn struct {
	Repo   string `json:"repo" jsonschema:"owner/name"`
	Number int64  `json:"number" jsonschema:"pull request number"`
}

type DiffIn struct {
	PRIn
	Path string `json:"path" jsonschema:"changed file path"`
}

type ReadIn struct {
	PRIn
	Path  string `json:"path" jsonschema:"repository-relative file path"`
	Side  string `json:"side,omitempty" jsonschema:"head (default) or base"`
	Start int    `json:"start_line,omitempty" jsonschema:"first line, 1-based"`
	End   int    `json:"end_line,omitempty" jsonschema:"last line, inclusive"`
}

type LsIn struct {
	PRIn
	Path string `json:"path,omitempty" jsonschema:"directory, empty for the root"`
}

type NoteIn struct {
	PRIn
	Kind string `json:"kind" jsonschema:"finding, summary, decision or question"`
	Text string `json:"text" jsonschema:"what to record on the timeline"`
}

type CommentIn struct {
	PRIn
	Body string `json:"body" jsonschema:"markdown"`
}

type ReviewIn struct {
	PRIn
	Event    string    `json:"event" jsonschema:"COMMENT, REQUEST_CHANGES or APPROVED"`
	Summary  string    `json:"summary" jsonschema:"markdown shown at the top of the review"`
	Comments []Comment `json:"comments,omitempty"`
}

func readOnly() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{ReadOnlyHint: true}
}

func (s *Service) Register(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{Name: "list_repos", Annotations: readOnly(),
		Description: "Repositories the token can see, most recently active first."}, s.listRepos)
	mcp.AddTool(srv, &mcp.Tool{Name: "list_pulls", Annotations: readOnly(),
		Description: "Pull requests in a repository."}, s.listPulls)
	mcp.AddTool(srv, &mcp.Tool{Name: "get_pull", Annotations: readOnly(),
		Description: "Load a pull request: metadata, description, changed files and the annotated diff. Starts the review session on the dashboard."}, s.getPull)
	mcp.AddTool(srv, &mcp.Tool{Name: "get_diff", Annotations: readOnly(),
		Description: "Annotated diff of one changed file."}, s.getDiff)
	mcp.AddTool(srv, &mcp.Tool{Name: "read_file", Annotations: readOnly(),
		Description: "A repository file with line numbers, at the PR head (default) or base."}, s.readFile)
	mcp.AddTool(srv, &mcp.Tool{Name: "list_dir", Annotations: readOnly(),
		Description: "Directory listing at the PR head."}, s.listDir)
	mcp.AddTool(srv, &mcp.Tool{Name: "get_comments", Annotations: readOnly(),
		Description: "Reviews, inline comments (with resolved state) and conversation already on the PR."}, s.getComments)
	mcp.AddTool(srv, &mcp.Tool{Name: "review_status", Annotations: readOnly(),
		Description: "Follow-up: which comments the author resolved, commits since the review, CI and merge state."}, s.reviewStatus)
	mcp.AddTool(srv, &mcp.Tool{Name: "note",
		Description: "Record a finding, summary, decision or question on the dashboard timeline. Does not touch Gitea."}, s.note)
	mcp.AddTool(srv, &mcp.Tool{Name: "post_review",
		Description: "Post a review with inline comments. Each comment is rendered as: [severity] Issue, Fix (a one-click suggestion when replace_line is true), Why, Why not the alternative. Positions are checked against the diff first. Only when the human asked."}, s.postReview)
	mcp.AddTool(srv, &mcp.Tool{Name: "post_comment",
		Description: "Post a plain comment on the PR conversation. Only when the human asked."}, s.postComment)
}

func (s *Service) listRepos(ctx context.Context, _ *mcp.CallToolRequest, in ReposIn) (*mcp.CallToolResult, any, error) {
	repos, err := s.g.Repos(ctx)
	if err != nil {
		return fail(err), nil, nil
	}
	q := strings.ToLower(in.Query)
	var b strings.Builder
	for _, r := range repos {
		if q != "" && !strings.Contains(strings.ToLower(r.FullName+" "+r.Description), q) {
			continue
		}
		vis := "public"
		if r.Private {
			vis = "private"
		}
		fmt.Fprintf(&b, "%s  (%s, %d open PRs, updated %s)", r.FullName, vis, r.OpenPRs, r.UpdatedAt.Format("2006-01-02"))
		if r.Description != "" {
			b.WriteString("  " + clip(r.Description, 80))
		}
		b.WriteByte('\n')
	}
	if b.Len() == 0 {
		return text("no repositories"), nil, nil
	}
	return text(b.String()), nil, nil
}

func (s *Service) listPulls(ctx context.Context, _ *mcp.CallToolRequest, in PullsIn) (*mcp.CallToolResult, any, error) {
	owner, repo, err := splitRepo(in.Repo)
	if err != nil {
		return fail(err), nil, nil
	}
	state := or(in.State, "open")
	pulls, err := s.g.Pulls(ctx, owner, repo, state)
	if err != nil {
		return fail(err), nil, nil
	}
	if len(pulls) == 0 {
		return text(fmt.Sprintf("no %s pull requests in %s/%s", state, owner, repo)), nil, nil
	}
	var b strings.Builder
	for _, p := range pulls {
		flags := ""
		if p.Draft {
			flags += " [draft]"
		}
		if p.Merged {
			flags += " [merged]"
		}
		fmt.Fprintf(&b, "#%d %s%s  %s, %s -> %s, updated %s\n",
			p.Number, p.Title, flags, p.User.Login, p.Head.Ref, p.Base.Ref, p.UpdatedAt.Format("2006-01-02 15:04"))
	}
	return text(b.String()), nil, nil
}

func (s *Service) pr(ctx context.Context, in PRIn) (string, string, *pr, error) {
	owner, repo, err := splitRepo(in.Repo)
	if err != nil {
		return "", "", nil, err
	}
	if in.Number <= 0 {
		return "", "", nil, fmt.Errorf("number must be positive")
	}
	p, err := s.load(ctx, owner, repo, in.Number, false)
	return owner, repo, p, err
}

func (s *Service) getPull(ctx context.Context, _ *mcp.CallToolRequest, in PRIn) (*mcp.CallToolResult, any, error) {
	owner, repo, err := splitRepo(in.Repo)
	if err != nil {
		return fail(err), nil, nil
	}
	p, err := s.load(ctx, owner, repo, in.Number, true)
	if err != nil {
		return fail(err), nil, nil
	}
	pull := p.pull

	var b strings.Builder
	fmt.Fprintf(&b, "# %s/%s#%d: %s\n", owner, repo, pull.Number, pull.Title)
	fmt.Fprintf(&b, "Author: %s\nState: %s", pull.User.Login, pull.State)
	if pull.Merged {
		b.WriteString(" (merged)")
	}
	if pull.Draft {
		b.WriteString(" DRAFT")
	}
	fmt.Fprintf(&b, "\nBranch: %s (%s) -> %s (%s)\n", pull.Head.Ref, clip(pull.Head.SHA, 10), pull.Base.Ref, clip(pull.Base.SHA, 10))
	if len(pull.Labels) > 0 {
		names := make([]string, len(pull.Labels))
		for i, l := range pull.Labels {
			names[i] = l.Name
		}
		fmt.Fprintf(&b, "Labels: %s\n", strings.Join(names, ", "))
	}
	fmt.Fprintf(&b, "URL: %s\n\n## Description\n%s\n\n", s.g.LocalURL(pull.HTMLURL), strings.TrimSpace(or(pull.Body, "(none)")))

	fmt.Fprintf(&b, "## Changed files (%d)\n", len(p.files))
	for _, f := range p.files {
		fmt.Fprintf(&b, "- %s [%s] +%d -%d", f.Filename, f.Status, f.Additions, f.Deletions)
		if f.PreviousFilename != "" {
			fmt.Fprintf(&b, " (was %s)", f.PreviousFilename)
		}
		b.WriteByte('\n')
	}

	all := diff.AnnotateAll(p.diffs)
	if len(all) <= s.cfg.DiffLimit {
		fmt.Fprintf(&b, "\n## Diff (OLD NEW |marker text)\n%s\n", all)
	} else {
		fmt.Fprintf(&b, "\n## Diff\n%d KB, too big to inline. Use get_diff(path) per file.\n", len(all)/1024)
	}

	s.log(owner, repo, in.Number, p, store.Event{Kind: "tool", Tool: "get_pull",
		Title: fmt.Sprintf("Loaded PR: %d files, +%d -%d", len(p.files), pull.Additions, pull.Deletions)})
	return text(b.String()), nil, nil
}

func (s *Service) getDiff(ctx context.Context, _ *mcp.CallToolRequest, in DiffIn) (*mcp.CallToolResult, any, error) {
	owner, repo, p, err := s.pr(ctx, in.PRIn)
	if err != nil {
		return fail(err), nil, nil
	}
	f := p.byPath[in.Path]
	if f == nil {
		var names []string
		for k := range p.byPath {
			names = append(names, k)
		}
		return fail(fmt.Errorf("%q is not in this PR; changed files: %s", in.Path, strings.Join(names, ", "))), nil, nil
	}
	s.log(owner, repo, in.Number, p, store.Event{Kind: "tool", Tool: "get_diff", Title: "Read diff of " + in.Path})
	return text(diff.Annotate(*f)), nil, nil
}

func (s *Service) readFile(ctx context.Context, _ *mcp.CallToolRequest, in ReadIn) (*mcp.CallToolResult, any, error) {
	owner, repo, p, err := s.pr(ctx, in.PRIn)
	if err != nil {
		return fail(err), nil, nil
	}
	ref, side := p.pull.Head.SHA, "head"
	if in.Side == "base" {
		ref, side = p.pull.Base.SHA, "base"
	}
	content, err := s.g.Raw(ctx, owner, repo, in.Path, ref)
	if err != nil {
		return fail(fmt.Errorf("%s@%s: %w", in.Path, side, err)), nil, nil
	}

	lines := strings.Split(content, "\n")
	total := len(lines)
	start, end := 1, total
	if in.Start > 0 {
		start = in.Start
	}
	if in.End > 0 && in.End < end {
		end = in.End
	}
	if start > total {
		return fail(fmt.Errorf("%s has %d lines", in.Path, total)), nil, nil
	}

	var b strings.Builder
	w := len(fmt.Sprint(total))
	cut := false
	for i := start; i <= end; i++ {
		if b.Len() > readLimit {
			cut = true
			end = i - 1
			break
		}
		fmt.Fprintf(&b, "%*d | %s\n", w, i, lines[i-1])
	}
	head := fmt.Sprintf("%s@%s lines %d-%d of %d", in.Path, side, start, end, total)
	if cut {
		head += " (truncated, ask for a smaller range)"
	}
	s.log(owner, repo, in.Number, p, store.Event{Kind: "tool", Tool: "read_file",
		Title: fmt.Sprintf("Read %s@%s %d-%d", in.Path, side, start, end)})
	return text(head + "\n" + b.String()), nil, nil
}

func (s *Service) listDir(ctx context.Context, _ *mcp.CallToolRequest, in LsIn) (*mcp.CallToolResult, any, error) {
	owner, repo, p, err := s.pr(ctx, in.PRIn)
	if err != nil {
		return fail(err), nil, nil
	}
	ents, err := s.g.Ls(ctx, owner, repo, in.Path, p.pull.Head.SHA)
	if err != nil {
		return fail(err), nil, nil
	}
	var b strings.Builder
	for _, e := range ents {
		if e.Type == "dir" {
			fmt.Fprintf(&b, "d %s/\n", e.Path)
		} else {
			fmt.Fprintf(&b, "f %s (%d)\n", e.Path, e.Size)
		}
	}
	s.log(owner, repo, in.Number, p, store.Event{Kind: "tool", Tool: "list_dir", Title: "Listed " + or(in.Path, "/")})
	return text(or(b.String(), "(empty)")), nil, nil
}

func (s *Service) getComments(ctx context.Context, _ *mcp.CallToolRequest, in PRIn) (*mcp.CallToolResult, any, error) {
	owner, repo, p, err := s.pr(ctx, in)
	if err != nil {
		return fail(err), nil, nil
	}
	reviews, err := s.g.Reviews(ctx, owner, repo, in.Number)
	if err != nil {
		return fail(err), nil, nil
	}
	conv, err := s.g.IssueComments(ctx, owner, repo, in.Number)
	if err != nil {
		return fail(err), nil, nil
	}

	var b strings.Builder
	for _, r := range reviews {
		stale := ""
		if r.Stale {
			stale = " [stale: new commits since]"
		}
		fmt.Fprintf(&b, "## Review #%d by %s: %s (%s)%s\n%s\n",
			r.ID, r.User.Login, r.State, r.SubmittedAt.Format("2006-01-02 15:04"), stale, or(r.Body, "(no summary)"))
		if r.CommentsCount == 0 {
			b.WriteByte('\n')
			continue
		}
		cs, err := s.g.ReviewComments(ctx, owner, repo, in.Number, r.ID)
		if err != nil {
			return fail(err), nil, nil
		}
		for _, c := range cs {
			line, side := c.Line()
			state := "open"
			if c.Resolver != nil {
				state = "resolved by " + c.Resolver.Login
			}
			fmt.Fprintf(&b, "  - [%s] %s:%s:%d (%s): %s\n", state, c.Path, side, line, c.User.Login,
				clip(strings.ReplaceAll(c.Body, "\n", " "), 300))
		}
		b.WriteByte('\n')
	}
	for _, c := range conv {
		fmt.Fprintf(&b, "## Comment by %s (%s)\n%s\n\n", c.User.Login, c.CreatedAt.Format("2006-01-02 15:04"), c.Body)
	}
	s.log(owner, repo, in.Number, p, store.Event{Kind: "tool", Tool: "get_comments", Title: "Read existing comments"})
	return text(or(b.String(), "no reviews or comments yet")), nil, nil
}

func (s *Service) reviewStatus(ctx context.Context, _ *mcp.CallToolRequest, in PRIn) (*mcp.CallToolResult, any, error) {
	owner, repo, p, err := s.pr(ctx, in)
	if err != nil {
		return fail(err), nil, nil
	}
	st, err := s.Status(ctx, owner, repo, in.Number)
	if err != nil {
		return fail(err), nil, nil
	}
	s.log(owner, repo, in.Number, p, store.Event{Kind: "tool", Tool: "review_status",
		Title: fmt.Sprintf("Checked status: %d/%d comments resolved", st.Totals.Resolved, st.Totals.Comments)})
	return text(st.Render()), nil, nil
}

func (s *Service) note(ctx context.Context, _ *mcp.CallToolRequest, in NoteIn) (*mcp.CallToolResult, any, error) {
	owner, repo, p, err := s.pr(ctx, in.PRIn)
	if err != nil {
		return fail(err), nil, nil
	}
	kind := or(strings.ToLower(in.Kind), "note")
	first, _, _ := strings.Cut(strings.TrimSpace(in.Text), "\n")
	s.log(owner, repo, in.Number, p, store.Event{Kind: "note", Tool: kind, Title: clip(first, 160), Detail: in.Text})
	return text("recorded " + kind), nil, nil
}

func (s *Service) postReview(ctx context.Context, _ *mcp.CallToolRequest, in ReviewIn) (*mcp.CallToolResult, any, error) {
	owner, repo, p, err := s.pr(ctx, in.PRIn)
	if err != nil {
		return fail(err), nil, nil
	}
	event := strings.ToUpper(strings.TrimSpace(in.Event))
	if event == "APPROVE" {
		event = "APPROVED"
	}
	switch event {
	case "APPROVED", "REQUEST_CHANGES", "COMMENT":
	default:
		return fail(fmt.Errorf("event must be COMMENT, REQUEST_CHANGES or APPROVED")), nil, nil
	}

	var bad []string
	var out []gitea.NewComment
	for _, c := range in.Comments {
		if err := c.check(p); err != nil {
			bad = append(bad, err.Error())
			continue
		}
		nc := gitea.NewComment{Path: c.Path, Body: c.Render()}
		if c.Side == "old" {
			nc.OldPosition = int64(c.Line)
		} else {
			nc.NewPosition = int64(c.Line)
		}
		out = append(out, nc)
	}
	if len(bad) > 0 {
		return fail(fmt.Errorf("not posted, fix these and retry:\n- %s", strings.Join(bad, "\n- "))), nil, nil
	}

	r, err := s.g.CreateReview(ctx, owner, repo, in.Number, gitea.NewReview{
		Body: in.Summary, Event: event, CommitID: p.pull.Head.SHA, Comments: out,
	})
	if err != nil {
		s.log(owner, repo, in.Number, p, store.Event{Kind: "error", Tool: "post_review", Title: "Gitea rejected the review", Detail: err.Error()})
		return fail(err), nil, nil
	}

	url := s.g.LocalURL(r.HTMLURL)
	s.store.Update(store.ID(owner, repo, in.Number), nil, func(sess *store.Session) {
		sess.Posted = append(sess.Posted, store.Posted{At: time.Now(), Type: "review", ID: r.ID, Event: event,
			Comments: len(out), URL: url, HeadSHA: p.pull.Head.SHA})
	})
	type posted struct {
		Path string `json:"path"`
		Line int    `json:"line"`
		Side string `json:"side"`
		Body string `json:"body"`
	}
	shown := make([]posted, len(in.Comments))
	for i, c := range in.Comments {
		shown[i] = posted{c.Path, c.Line, or(c.Side, "new"), c.Render()}
	}
	detail, _ := json.MarshalIndent(map[string]any{"summary": in.Summary, "comments": shown}, "", "  ")
	s.log(owner, repo, in.Number, p, store.Event{Kind: "post", Tool: "post_review",
		Title: fmt.Sprintf("Posted %s review with %d inline comments", event, len(out)), Detail: string(detail)})
	return text(fmt.Sprintf("posted review #%d (%s) with %d inline comments: %s", r.ID, event, len(out), url)), nil, nil
}

func (s *Service) postComment(ctx context.Context, _ *mcp.CallToolRequest, in CommentIn) (*mcp.CallToolResult, any, error) {
	owner, repo, p, err := s.pr(ctx, in.PRIn)
	if err != nil {
		return fail(err), nil, nil
	}
	c, err := s.g.Comment(ctx, owner, repo, in.Number, in.Body)
	if err != nil {
		s.log(owner, repo, in.Number, p, store.Event{Kind: "error", Tool: "post_comment", Title: "Gitea rejected the comment", Detail: err.Error()})
		return fail(err), nil, nil
	}
	url := s.g.LocalURL(c.HTMLURL)
	s.store.Update(store.ID(owner, repo, in.Number), nil, func(sess *store.Session) {
		sess.Posted = append(sess.Posted, store.Posted{At: time.Now(), Type: "comment", ID: c.ID, URL: url, HeadSHA: p.pull.Head.SHA})
	})
	s.log(owner, repo, in.Number, p, store.Event{Kind: "post", Tool: "post_comment", Title: "Posted comment", Detail: in.Body})
	return text("posted comment: " + url), nil, nil
}
