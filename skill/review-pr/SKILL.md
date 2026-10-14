---
name: review-pr
description: Review a Gitea pull request with the prr MCP server. Load the PR, read the code, present findings, post the review when asked. Use for "review pr", "review PR #12", "look at the pull request", or owner/repo#N.
---

# Review a Gitea PR

Use the `prr` MCP tools. Every call shows up on the dashboard, so keep the
timeline readable with `note`.

1. Find the PR. With `owner/repo#N`, call `get_pull` directly. Otherwise
   `list_repos`, `list_pulls`, and ask which one.

2. Understand the change. `get_pull` gives metadata, the file list and the
   annotated diff (OLD and NEW number columns, then `+`/`-`/space). For big
   PRs use `get_diff` per file. Read callers, types, tests and config with
   `read_file` / `list_dir` whenever the diff alone cannot prove something
   is a bug. `get_comments` first, so nothing already raised is repeated.

3. Record findings. For each real problem: `note(kind="finding")` with
   severity (blocker/major/minor/nit), `path:line`, the concrete failure and
   the fix. Style only when it hides a real problem.

4. Present to the user: what the PR does, the verdict, then findings most
   severe first. `note(kind="summary")` with the same text.

5. Post only when asked. On "post it": `post_review` with `event` matching
   the verdict (COMMENT, REQUEST_CHANGES, APPROVED), the summary, and one
   comment per finding. Each comment has these fields, rendered by the server
   in this order:
   - `issue`: the problem in its simplest form, one or two plain sentences
   - `fix`: copy-pasteable code; `replace_line: true` when it replaces
     exactly the commented line (one-click suggestion), otherwise a snippet
     with `fix_lang`
   - `why`: why we do it this way
   - `why_not`: the tempting alternative and why not, when there is one
   plus `severity`. For a general remark use `post_comment`.

6. Follow up. `review_status` shows which comments the author resolved, what
   was pushed since, and CI. Re-read changed files before calling a comment
   addressed.

Line references: NEW column for added and context lines (`side: "new"`),
OLD column for removed lines (`side: "old"`). `post_review` rejects
positions outside the diff.
