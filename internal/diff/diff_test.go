package diff

import (
	"strings"
	"testing"
)

const sample = `diff --git a/src/a.ts b/src/a.ts
index 1111111..2222222 100644
--- a/src/a.ts
+++ b/src/a.ts
@@ -10,4 +10,5 @@ export function x() {
   const a = 1;
-  const b = 2;
+  const b = 3;
+  const c = 4;
   return a + b;
 }
diff --git a/new file.txt b/new file.txt
new file mode 100644
index 0000000..3333333
--- /dev/null
+++ b/new file.txt
@@ -0,0 +1,2 @@
+hello
+world
\ No newline at end of file
diff --git a/img.png b/img.png
new file mode 100644
Binary files /dev/null and b/img.png differ
diff --git a/old.ts b/renamed.ts
similarity index 90%
rename from old.ts
rename to renamed.ts
`

func TestParse(t *testing.T) {
	files := Parse(sample)
	if len(files) != 4 {
		t.Fatalf("want 4 files, got %d", len(files))
	}
	f := files[0]
	if f.Path != "src/a.ts" || f.Status != "modified" || f.Additions != 2 || f.Deletions != 1 {
		t.Errorf("file0 = %+v", f)
	}
	if files[1].Path != "new file.txt" || files[1].Status != "added" {
		t.Errorf("file1 = %+v", files[1])
	}
	if !files[2].Binary {
		t.Errorf("file2 should be binary")
	}
	if files[3].Status != "renamed" || files[3].OldPath != "old.ts" || files[3].Path != "renamed.ts" {
		t.Errorf("file3 = %+v", files[3])
	}
}

func TestLineNumbers(t *testing.T) {
	f := Parse(sample)[0]
	want := []Line{
		{Context, 10, 10, "  const a = 1;"},
		{Del, 11, 0, "  const b = 2;"},
		{Add, 0, 11, "  const b = 3;"},
		{Add, 0, 12, "  const c = 4;"},
		{Context, 12, 13, "  return a + b;"},
		{Context, 13, 14, "}"},
	}
	got := f.Hunks[0].Lines
	if len(got) != len(want) {
		t.Fatalf("want %d lines, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d: want %+v got %+v", i, want[i], got[i])
		}
	}
	nl, ol := Lines(f)
	for _, n := range []int{10, 11, 12, 13, 14} {
		if !nl[n] {
			t.Errorf("new line %d should be commentable", n)
		}
	}
	if nl[15] || !ol[11] || ol[14] {
		t.Errorf("commentable sets wrong: new=%v old=%v", nl, ol)
	}
}

func TestAnnotate(t *testing.T) {
	out := Annotate(Parse(sample)[0])
	for _, want := range []string{
		"### src/a.ts (modified, +2 -1)",
		"   11     - |-  const b = 2;",
		"    -    11 |+  const b = 3;",
		"   12    13 |   return a + b;",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}
