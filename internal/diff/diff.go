package diff

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type Kind int

const (
	Context Kind = iota
	Add
	Del
	Meta
)

type Line struct {
	Kind  Kind
	OldNo int
	NewNo int
	Text  string
}

type Hunk struct {
	Header string
	Lines  []Line
}

type File struct {
	Path      string
	OldPath   string
	Status    string
	Binary    bool
	Hunks     []Hunk
	Additions int
	Deletions int
}

var (
	hunkRe = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)
	gitRe  = regexp.MustCompile(`^diff --git a/(.*) b/(.*)$`)
)

func Parse(text string) []File {
	var files []File
	var f *File
	var h *Hunk
	var oldNo, newNo int

	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")

		if strings.HasPrefix(line, "diff --git ") {
			files = append(files, File{Status: "modified"})
			f = &files[len(files)-1]
			h = nil
			if m := gitRe.FindStringSubmatch(line); m != nil {
				f.Path = m[2]
				if m[1] != m[2] {
					f.OldPath = m[1]
				}
			} else {
				f.Path = strings.TrimPrefix(line, "diff --git ")
			}
			continue
		}
		if f == nil {
			continue
		}

		if h == nil {
			switch {
			case strings.HasPrefix(line, "new file mode"):
				f.Status = "added"
			case strings.HasPrefix(line, "deleted file mode"):
				f.Status = "deleted"
			case strings.HasPrefix(line, "rename from "):
				f.Status = "renamed"
				f.OldPath = line[len("rename from "):]
			case strings.HasPrefix(line, "rename to "):
				f.Path = line[len("rename to "):]
			case strings.HasPrefix(line, "Binary files"),
				strings.HasPrefix(line, "GIT binary patch"):
				f.Binary = true
			case strings.HasPrefix(line, "+++ b/"):
				f.Path = line[len("+++ b/"):]
			case strings.HasPrefix(line, "--- a/") && f.Status != "renamed":
				if a := line[len("--- a/"):]; a != f.Path {
					f.OldPath = a
				}
			}
		}

		if m := hunkRe.FindStringSubmatch(line); m != nil {
			oldNo, _ = strconv.Atoi(m[1])
			newNo, _ = strconv.Atoi(m[2])
			f.Hunks = append(f.Hunks, Hunk{Header: line})
			h = &f.Hunks[len(f.Hunks)-1]
			continue
		}
		if h == nil {
			continue
		}

		switch {
		case line == "":
			/* git emits a bare empty line for an empty context line */
			h.Lines = append(h.Lines, Line{Context, oldNo, newNo, ""})
			oldNo++
			newNo++
		case line[0] == '+':
			h.Lines = append(h.Lines, Line{Add, 0, newNo, line[1:]})
			newNo++
			f.Additions++
		case line[0] == '-':
			h.Lines = append(h.Lines, Line{Del, oldNo, 0, line[1:]})
			oldNo++
			f.Deletions++
		case line[0] == ' ':
			h.Lines = append(h.Lines, Line{Context, oldNo, newNo, line[1:]})
			oldNo++
			newNo++
		case line[0] == '\\':
			h.Lines = append(h.Lines, Line{Meta, 0, 0, line})
		default:
			h = nil
		}
	}
	return files
}

func col(n int) string {
	if n == 0 {
		return "    -"
	}
	return fmt.Sprintf("%5d", n)
}

/*
 * Two number columns, old and new, then the usual +/-/space marker.
 * Reviewers cite the NEW column for added and context lines and the
 * OLD column for removed ones; that maps 1:1 onto what Gitea wants
 * in new_position/old_position.
 */
func Annotate(f File) string {
	var b strings.Builder

	name := f.Path
	if f.Status == "renamed" && f.OldPath != "" {
		name = f.OldPath + " -> " + f.Path
	}
	fmt.Fprintf(&b, "### %s (%s, +%d -%d)\n", name, f.Status, f.Additions, f.Deletions)

	if f.Binary {
		b.WriteString("(binary)")
		return b.String()
	}
	if len(f.Hunks) == 0 {
		b.WriteString("(no text changes)")
		return b.String()
	}

	b.WriteString("  OLD   NEW |\n")
	for _, h := range f.Hunks {
		b.WriteString(h.Header + "\n")
		for _, l := range h.Lines {
			switch l.Kind {
			case Add:
				fmt.Fprintf(&b, "%s %s |+%s\n", col(0), col(l.NewNo), l.Text)
			case Del:
				fmt.Fprintf(&b, "%s %s |-%s\n", col(l.OldNo), col(0), l.Text)
			case Meta:
				fmt.Fprintf(&b, "%s %s |%s\n", col(0), col(0), l.Text)
			default:
				fmt.Fprintf(&b, "%s %s | %s\n", col(l.OldNo), col(l.NewNo), l.Text)
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func AnnotateAll(files []File) string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = Annotate(f)
	}
	return strings.Join(out, "\n\n")
}

func Lines(f File) (newl, oldl map[int]bool) {
	newl = map[int]bool{}
	oldl = map[int]bool{}
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.NewNo > 0 {
				newl[l.NewNo] = true
			}
			if l.OldNo > 0 {
				oldl[l.OldNo] = true
			}
		}
	}
	return
}
