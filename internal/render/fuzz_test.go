package render

import "testing"

// FuzzMarkdownEscape: whatever the cluster puts in an event message, the
// Markdown report must not contain a raw <, > or newline outside a code
// span. The check reads the output the way a Markdown renderer does:
// outside code a backslash escapes the next character, inside code
// nothing is escaped.
func FuzzMarkdownEscape(f *testing.F) {
	for _, s := range []string{"<script>", "`<b>` <i>", "a|b", "``", "\n<", "`<b>", "`a` `<i>", "\\`>`"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := []rune(mdEscape(s))
		code := false
		for i := 0; i < len(out); i++ {
			switch r := out[i]; {
			case r == '\\' && !code:
				i++ // escaped character, whatever it is
			case r == '`':
				code = !code
			case !code && (r == '<' || r == '>' || r == '\n'):
				t.Fatalf("mdEscape(%q) = %q leaves %q outside code", s, string(out), r)
			}
		}
		if code {
			t.Fatalf("mdEscape(%q) = %q leaves a code span open", s, string(out))
		}
	})
}
