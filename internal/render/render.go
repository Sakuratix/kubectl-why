// Package render prints reports for people (colored text) or machines (JSON).
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Sakuratix/kubectl-why/internal/diagnose"
)

type Style struct {
	Color bool
}

func (s Style) c(code, text string) string {
	if !s.Color {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s Style) bold(t string) string  { return s.c("1", t) }
func (s Style) dim(t string) string   { return s.c("2", t) }
func (s Style) cyan(t string) string  { return s.c("36", t) }
func (s Style) green(t string) string { return s.c("32", t) }

func (s Style) sev(v diagnose.Severity) string {
	switch v {
	case diagnose.Crit:
		return s.c("1;31", "✗")
	case diagnose.Warn:
		return s.c("1;33", "!")
	case diagnose.Info:
		return s.c("1;34", "i")
	}
	return s.c("1;32", "✓")
}

// Text writes reports in a compact, scannable layout.
func Text(w io.Writer, st Style, reports []diagnose.Report) {
	for i, r := range reports {
		if i > 0 {
			fmt.Fprintln(w)
		}
		text(w, st, r, "")
	}
}

func text(w io.Writer, st Style, r diagnose.Report, pad string) {
	head := pad + st.dim(strings.ToLower(r.Kind)+"/") + st.bold(r.Name)
	meta := []string{r.Status, "ready " + r.Ready}
	if r.Restarts > 0 {
		meta = append(meta, fmt.Sprintf("restarts %d", r.Restarts))
	}
	meta = append(meta, "age "+r.Age)
	if r.Node != "" {
		meta = append(meta, "node "+r.Node)
	}
	fmt.Fprintf(w, "%s  %s\n", head, st.dim(strings.Join(meta, " · ")))

	for _, f := range r.Findings {
		title := f.Title
		if f.Container != "" {
			title = st.cyan("["+f.Container+"]") + " " + title
		}
		fmt.Fprintf(w, "%s  %s %s\n", pad, st.sev(f.Severity), title)
		if f.Detail != "" {
			wrap(w, pad+"    ", f.Detail, st.dim)
		}
		for _, e := range f.Evidence {
			fmt.Fprintf(w, "%s    %s %s\n", pad, st.dim("·"), e)
		}
		for _, h := range f.Hints {
			fmt.Fprintf(w, "%s    %s %s\n", pad, st.green("→"), h)
		}
	}
	if len(r.Warnings) > 0 {
		fmt.Fprintf(w, "%s  %s\n", pad, st.dim("other recent warnings:"))
		for _, e := range r.Warnings {
			fmt.Fprintf(w, "%s    %s\n", pad, st.dim(e))
		}
	}
	for _, c := range r.Children {
		fmt.Fprintln(w)
		text(w, st, c, pad+"  ")
	}
}

func wrap(w io.Writer, pad, s string, style func(string) string) {
	const width = 96
	line := ""
	for _, word := range strings.Fields(s) {
		if len(line)+len(word)+1 > width && line != "" {
			fmt.Fprintln(w, pad+style(line))
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += word
	}
	if line != "" {
		fmt.Fprintln(w, pad+style(line))
	}
}

// JSON writes reports as an indented JSON array.
func JSON(w io.Writer, reports []diagnose.Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(reports)
}
