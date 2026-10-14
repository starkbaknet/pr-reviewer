package review

import (
	"fmt"
	"strings"

	"prr/internal/diff"
)

/*
 * An inline review comment. The model fills the fields, we do the
 * formatting, so every comment on a PR reads the same way: what is
 * wrong, the code that fixes it, why, and why not the obvious
 * alternative.
 */
type Comment struct {
	Path        string `json:"path" jsonschema:"changed file path"`
	Line        int    `json:"line" jsonschema:"line number from the annotated diff"`
	Side        string `json:"side,omitempty" jsonschema:"new (default) for added/context lines, old for removed lines"`
	Severity    string `json:"severity" jsonschema:"blocker, major, minor or nit"`
	Issue       string `json:"issue" jsonschema:"the problem in its simplest form: what is wrong and what goes wrong because of it"`
	Fix         string `json:"fix" jsonschema:"copy-pasteable code. With replace_line it must be the complete replacement for exactly the commented line"`
	ReplaceLine bool   `json:"replace_line,omitempty" jsonschema:"fix replaces exactly the commented line; rendered as a one-click Gitea suggestion"`
	FixLang     string `json:"fix_lang,omitempty" jsonschema:"code fence language when replace_line is false"`
	Why         string `json:"why" jsonschema:"why we do it this way"`
	WhyNot      string `json:"why_not,omitempty" jsonschema:"the tempting alternative and why we do not do that"`
}

func (c Comment) check(p *pr) error {
	where := fmt.Sprintf("%s:%d", c.Path, c.Line)
	f := p.byPath[c.Path]
	if f == nil {
		return fmt.Errorf("%s: not a changed file in this PR", where)
	}
	newl, oldl := diff.Lines(*f)
	if c.Side == "old" && !oldl[c.Line] || c.Side != "old" && !newl[c.Line] {
		return fmt.Errorf("%s (%s side): line is not in the diff", where, or(c.Side, "new"))
	}
	if strings.TrimSpace(c.Issue) == "" || strings.TrimSpace(c.Why) == "" {
		return fmt.Errorf("%s: issue and why are required", where)
	}
	if c.ReplaceLine && strings.Contains(strings.TrimRight(c.Fix, "\n"), "\n") {
		return fmt.Errorf("%s: replace_line fix must be one line; use replace_line=false for a snippet", where)
	}
	return nil
}

func (c Comment) Render() string {
	var b strings.Builder

	fmt.Fprintf(&b, "**[%s] Issue:** %s\n\n", or(strings.ToLower(c.Severity), "minor"), strings.TrimSpace(c.Issue))

	if fix := strings.TrimRight(c.Fix, "\n"); fix != "" {
		lang := c.FixLang
		if c.ReplaceLine {
			lang = "suggestion"
		}
		fmt.Fprintf(&b, "**Fix:**\n```%s\n%s\n```\n\n", lang, fix)
	}

	fmt.Fprintf(&b, "**Why:** %s\n", strings.TrimSpace(c.Why))
	if w := strings.TrimSpace(c.WhyNot); w != "" {
		fmt.Fprintf(&b, "\n**Why not the alternative:** %s\n", w)
	}
	return strings.TrimRight(b.String(), "\n")
}
