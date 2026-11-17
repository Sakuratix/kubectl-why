package render

import "testing"

func TestMarkdownEscape(t *testing.T) {
	cases := map[string]string{
		`<script>alert(1)</script>`:           `&lt;script&gt;alert(1)&lt;/script&gt;`,
		"run `kubectl logs <pod> --previous`": "run `kubectl logs <pod> --previous`",
		"a | b * c_d [x]":                     `a \| b \* c\_d \[x\]`,
		"two\nlines":                          "two lines",
		"unmatched `<b> tick":                 "unmatched \\`&lt;b&gt; tick",
	}
	for in, want := range cases {
		if got := mdEscape(in); got != want {
			t.Errorf("mdEscape(%q) = %q, want %q", in, got, want)
		}
	}
}
