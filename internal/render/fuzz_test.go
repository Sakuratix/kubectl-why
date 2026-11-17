package render

import (
	"strings"
	"testing"
)

// FuzzMarkdownEscape: whatever the cluster puts in an event message, the
// Markdown report must not contain a raw HTML tag outside code spans.
func FuzzMarkdownEscape(f *testing.F) {
	for _, s := range []string{"<script>", "`<b>` <i>", "a|b", "``", "\n<", "`<b>", "`a` `<i>"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := mdEscape(s)
		// split on backticks that are real code-span markers, not escaped ones
		code := false
		for _, part := range strings.Split(strings.ReplaceAll(out, "\\`", "\x00"), "`") {
			if !code && strings.ContainsAny(part, "<>\n") {
				t.Fatalf("mdEscape(%q) = %q leaves <, > or a newline outside code", s, out)
			}
			code = !code
		}
	})
}
