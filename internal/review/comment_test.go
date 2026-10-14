package review

import (
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	c := Comment{
		Severity: "major", Issue: "Cache grows forever.",
		Fix: "    const t = T[lang];", ReplaceLine: true,
		Why: "One string replace.", WhyNot: "LRU adds a dependency for nothing.",
	}
	got := c.Render()
	want := []string{
		"**[major] Issue:** Cache grows forever.",
		"**Fix:**\n```suggestion\n    const t = T[lang];\n```",
		"**Why:** One string replace.",
		"**Why not the alternative:** LRU adds a dependency for nothing.",
	}
	last := -1
	for _, w := range want {
		i := strings.Index(got, w)
		if i < 0 {
			t.Fatalf("missing %q in:\n%s", w, got)
		}
		if i < last {
			t.Fatalf("%q out of order in:\n%s", w, got)
		}
		last = i
	}

	got = Comment{Issue: "x", Fix: "a\nb", FixLang: "ts", Why: "y"}.Render()
	if !strings.HasPrefix(got, "**[minor]") {
		t.Errorf("severity should default to minor: %s", got)
	}
	if !strings.Contains(got, "```ts\na\nb\n```") || strings.Contains(got, "Why not") {
		t.Errorf("bad snippet render:\n%s", got)
	}
}
