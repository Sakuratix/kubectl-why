// kubectl-why explains in plain words why a pod or deployment is not healthy.
//
//	kubectl why pod/api-7f9c6
//	kubectl why deploy/api -n prod
//	kubectl why                # every unhealthy pod in the namespace
//	kubectl why -A             # ... in every namespace
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/Sakuratix/kubectl-why/internal/diagnose"
	"github.com/Sakuratix/kubectl-why/internal/kube"
	"github.com/Sakuratix/kubectl-why/internal/render"
)

var (
	// set by goreleaser; `go install` builds fill them from build info instead
	version = ""
	commit  = ""
	date    = ""
	now     = time.Now
)

// versionString reports what was built and from where, so a bug report can
// say exactly which binary misdiagnosed a pod.
func versionString() string {
	v, c, d, dirty := version, commit, date, false
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v == "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			v = bi.Main.Version
		}
		for _, s := range bi.Settings {
			switch {
			case s.Key == "vcs.revision" && c == "":
				c = s.Value
			case s.Key == "vcs.time" && d == "":
				d = s.Value
			case s.Key == "vcs.modified" && s.Value == "true":
				dirty = commit == ""
			}
		}
	}
	if v == "" {
		v = "dev"
	}
	out := "kubectl-why " + v
	var extra []string
	if c != "" {
		if len(c) > 12 {
			c = c[:12]
		}
		if dirty {
			c += "-dirty"
		}
		extra = append(extra, "commit "+c)
	}
	if d != "" {
		extra = append(extra, "built "+d)
	}
	extra = append(extra, runtime.Version(), runtime.GOOS+"/"+runtime.GOARCH)
	return out + " (" + strings.Join(extra, ", ") + ")"
}

const usage = `kubectl why: explain why a pod or deployment is unhealthy

Usage:
  kubectl why [TARGET...] [flags]

Targets:
  pod/NAME | NAME          one pod
  deploy/NAME              a deployment and its pods
  (none)                   every unhealthy pod in the namespace

Flags:
  -n, --namespace NS       namespace (default: from kubeconfig)
  -A, --all-namespaces     scan every namespace (only without targets)
      --context NAME       kubeconfig context
      --kubeconfig FILE    kubeconfig file
  -o, --output FORMAT      text (default), json, or markdown for tickets and PR comments
      --no-color           disable colors (also NO_COLOR=1; CLICOLOR_FORCE=1 forces them)
  -v, --version            print version

Exit codes: 0 healthy, 1 problems found, 2 error.
`

type opts struct {
	ns, ctx, kubeconfig, output string
	noColor, all                bool
	targets                     []string
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, nil))
}

func run(args []string, stdout, stderr io.Writer, r kube.Runner) int {
	o, err := parse(args)
	if errors.Is(err, errHelp) {
		fmt.Fprint(stdout, usage)
		return 0
	}
	if errors.Is(err, errVersion) {
		fmt.Fprintln(stdout, versionString())
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n\n%s", err, usage)
		return 2
	}
	if r == nil {
		k := kube.Kubectl{}
		if o.ctx != "" {
			k.Global = append(k.Global, "--context", o.ctx)
		}
		if o.kubeconfig != "" {
			k.Global = append(k.Global, "--kubeconfig", o.kubeconfig)
		}
		r = k
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	c := kube.Client{R: r, Namespace: o.ns, AllNamespaces: o.all}
	reports, err := collect(ctx, c, o.targets, now())
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	switch o.output {
	case "json":
		if err := render.JSON(stdout, reports); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 2
		}
	case "markdown", "md":
		render.Markdown(stdout, reports)
	default:
		render.Text(stdout, render.Style{Color: !o.noColor && colorOK(stdout), Namespaces: o.all}, reports)
	}
	for _, rep := range reports {
		if rep.Worst() >= diagnose.Warn {
			return 1
		}
	}
	return 0
}

var (
	errHelp    = errors.New("help")
	errVersion = errors.New("version")
)

// parse accepts flags anywhere, like kubectl does.
func parse(args []string) (opts, error) {
	o := opts{output: "text"}
	for i := 0; i < len(args); i++ {
		a := args[i]
		val := func() (string, error) {
			if k, v, ok := strings.Cut(a, "="); ok && strings.HasPrefix(k, "-") {
				return v, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("flag %s needs a value", a)
			}
			i++
			return args[i], nil
		}
		name, _, _ := strings.Cut(a, "=")
		var err error
		switch name {
		case "-h", "--help":
			return o, errHelp
		case "-v", "--version":
			return o, errVersion
		case "-n", "--namespace":
			o.ns, err = val()
		case "--context":
			o.ctx, err = val()
		case "--kubeconfig":
			o.kubeconfig, err = val()
		case "-o", "--output":
			o.output, err = val()
		case "-A", "--all-namespaces":
			o.all = true
		case "--no-color":
			o.noColor = true
		default:
			if strings.HasPrefix(a, "-") {
				return o, fmt.Errorf("unknown flag %s", a)
			}
			o.targets = append(o.targets, a)
		}
		if err != nil {
			return o, err
		}
	}
	switch o.output {
	case "text", "json", "markdown", "md":
	default:
		return o, fmt.Errorf("--output must be text, json or markdown, got %q", o.output)
	}
	if o.all && len(o.targets) > 0 {
		return o, errors.New("--all-namespaces scans the whole cluster; drop the targets or use -n")
	}
	// "kubectl why deploy api" -> deploy/api
	if len(o.targets) == 2 && !strings.Contains(o.targets[0], "/") && kindOf(o.targets[0]) != "" {
		o.targets = []string{o.targets[0] + "/" + o.targets[1]}
	}
	return o, nil
}

func kindOf(s string) string {
	switch strings.ToLower(s) {
	case "pod", "pods", "po":
		return "pod"
	case "deploy", "deployment", "deployments", "deploy.apps", "deployment.apps":
		return "deployment"
	}
	return ""
}

func collect(ctx context.Context, c kube.Client, targets []string, now time.Time) ([]diagnose.Report, error) {
	if len(targets) == 0 {
		return namespaceScan(ctx, c, now)
	}
	var out []diagnose.Report
	for _, t := range targets {
		kind, name := "pod", t
		if k, n, ok := strings.Cut(t, "/"); ok {
			kind, name = kindOf(k), n
			if kind == "" {
				return nil, fmt.Errorf("unsupported kind %q (use pod/ or deploy/)", k)
			}
		}
		switch kind {
		case "pod":
			p, err := c.Pod(ctx, name)
			if err != nil {
				return nil, err
			}
			ev, err := c.EventsFor(ctx, "Pod", name)
			if err != nil {
				return nil, err
			}
			out = append(out, diagnose.Pod(p, ev, now))
		case "deployment":
			d, err := c.Deployment(ctx, name)
			if err != nil {
				return nil, err
			}
			pods, err := c.Pods(ctx, d.Spec.Selector.MatchLabels)
			if err != nil {
				return nil, err
			}
			events, err := c.Events(ctx)
			if err != nil {
				return nil, err
			}
			var prs []diagnose.Report
			for _, p := range pods {
				prs = append(prs, diagnose.Pod(p, events[kube.EventKey(p.Metadata.Namespace, "Pod", p.Metadata.Name)], now))
			}
			out = append(out, diagnose.Deployment(d, prs, now))
		}
	}
	return out, nil
}

func namespaceScan(ctx context.Context, c kube.Client, now time.Time) ([]diagnose.Report, error) {
	pods, err := c.Pods(ctx, nil)
	if err != nil {
		return nil, err
	}
	events, err := c.Events(ctx)
	if err != nil {
		return nil, err
	}
	var out []diagnose.Report
	for _, p := range pods {
		r := diagnose.Pod(p, events[kube.EventKey(p.Metadata.Namespace, "Pod", p.Metadata.Name)], now)
		if r.Worst() >= diagnose.Warn || (r.Worst() == diagnose.Info && r.Restarts > 0) {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		kind, name, title := "Namespace", c.Namespace, fmt.Sprintf("All %d pods are healthy", len(pods))
		if name == "" {
			name = "current namespace"
		}
		if c.AllNamespaces {
			nss := map[string]bool{}
			for _, p := range pods {
				nss[p.Metadata.Namespace] = true
			}
			kind, name = "Cluster", "all namespaces"
			title = fmt.Sprintf("All %d pods in %d namespaces are healthy", len(pods), len(nss))
		}
		out = append(out, diagnose.Report{
			Kind: kind, Name: name, Status: "Healthy",
			Ready: fmt.Sprintf("%d pods", len(pods)), Age: "-",
			Findings: []diagnose.Finding{{Severity: diagnose.OK, Title: title}},
		})
	}
	return out, nil
}

func colorOK(w io.Writer) bool {
	if os.Getenv("CLICOLOR_FORCE") == "1" {
		return true
	}
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}
