package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/Sakuratix/kubectl-why/internal/diagnose"
)

var mdIcon = map[diagnose.Severity]string{
	diagnose.Crit: "🔴",
	diagnose.Warn: "🟡",
	diagnose.Info: "🔵",
	diagnose.OK:   "🟢",
}

// Markdown writes reports as GitHub-flavoured Markdown: something to paste
// into an incident ticket or post as a PR comment from a CI job. Hints become
// a task list so whoever picks the ticket up can tick them off.
func Markdown(w io.Writer, reports []diagnose.Report) {
	counts := map[diagnose.Severity]int{}
	for _, r := range reports {
		count(r, counts)
	}
	var parts []string
	for _, s := range []diagnose.Severity{diagnose.Crit, diagnose.Warn, diagnose.Info} {
		if counts[s] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[s], s))
		}
	}
	summary := "nothing wrong"
	if len(parts) > 0 {
		summary = strings.Join(parts, ", ")
	}
	fmt.Fprintf(w, "> **kubectl why** · %s\n", summary)
	for _, r := range reports {
		markdown(w, r, "###")
	}
}

func count(r diagnose.Report, c map[diagnose.Severity]int) {
	for _, f := range r.Findings {
		c[f.Severity]++
	}
	for _, ch := range r.Children {
		count(ch, c)
	}
}

func markdown(w io.Writer, r diagnose.Report, h string) {
	where := ""
	if r.Namespace != "" {
		where = fmt.Sprintf(" in `%s`", r.Namespace)
	}
	fmt.Fprintf(w, "\n%s %s %s/%s%s\n\n", h, mdIcon[r.Worst()], strings.ToLower(r.Kind), mdEscape(r.Name), where)

	meta := []string{"**" + r.Status + "**", "ready " + r.Ready}
	if r.Restarts > 0 {
		meta = append(meta, fmt.Sprintf("restarts %d", r.Restarts))
	}
	meta = append(meta, "age "+r.Age)
	if r.Node != "" {
		meta = append(meta, "node `"+r.Node+"`")
	}
	fmt.Fprintln(w, strings.Join(meta, " · "))

	for _, f := range r.Findings {
		title := mdEscape(f.Title)
		if f.Container != "" {
			title = "`" + f.Container + "` " + title
		}
		fmt.Fprintf(w, "\n%s **%s**\n", mdIcon[f.Severity], title)
		if f.Detail != "" {
			fmt.Fprintf(w, "\n%s\n", mdEscape(f.Detail))
		}
		if len(f.Evidence) > 0 {
			fmt.Fprintln(w)
			for _, e := range f.Evidence {
				fmt.Fprintf(w, "- %s\n", mdEscape(e))
			}
		}
		if len(f.Hints) > 0 {
			fmt.Fprintln(w, "\nNext steps:")
			for _, hint := range f.Hints {
				fmt.Fprintf(w, "- [ ] %s\n", mdEscape(hint))
			}
		}
	}
	if len(r.Warnings) > 0 {
		fmt.Fprintf(w, "\n<details><summary>Other recent warnings (%d)</summary>\n\n", len(r.Warnings))
		for _, e := range r.Warnings {
			fmt.Fprintf(w, "- %s\n", mdEscape(e))
		}
		fmt.Fprintln(w, "\n</details>")
	}
	for _, c := range r.Children {
		markdown(w, c, h+"#")
	}
}

// mdEscape keeps cluster-supplied text (event messages, termination
// messages) from turning into HTML or breaking the layout. Text inside
// matched backticks is left alone so commands stay copyable; an unmatched
// backtick is escaped, otherwise everything after it would be passed
// through raw.
func mdEscape(s string) string {
	ticks := strings.Count(s, "`")
	var b strings.Builder
	code, seen := false, 0
	for _, r := range s {
		switch {
		case r == '`':
			seen++
			if seen == ticks && ticks%2 == 1 {
				b.WriteString("\\`")
				continue
			}
			code = !code
			b.WriteRune(r)
		case code && r != '\n':
			b.WriteRune(r)
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == '\n':
			b.WriteByte(' ')
		case strings.ContainsRune(`\*_[]|#`, r):
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
