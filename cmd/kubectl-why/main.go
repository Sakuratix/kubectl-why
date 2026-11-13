// kubectl-why explains in plain words why a pod or deployment is not healthy.
//
//	kubectl why pod/api-7f9c6
//	kubectl why deploy/api -n prod
//	kubectl why                # every unhealthy pod in the namespace
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Sakuratix/kubectl-why/internal/diagnose"
	"github.com/Sakuratix/kubectl-why/internal/kube"
	"github.com/Sakuratix/kubectl-why/internal/render"
)

var (
	version = "dev" // set by goreleaser
	now     = time.Now
)

const usage = `kubectl why: explain why a pod or deployment is unhealthy

Usage:
  kubectl why [TARGET...] [flags]

Targets:
  pod/NAME | NAME          one pod
  deploy/NAME              a deployment and its pods
  (none)                   every unhealthy pod in the namespace

Flags:
  -n, --namespace NS       namespace (default: from kubeconfig)
      --context NAME       kubeconfig context
      --kubeconfig FILE    kubeconfig file
  -o, --output text|json   output format (default text)
      --no-color           disable colors (also NO_COLOR=1; CLICOLOR_FORCE=1 forces them)
  -v, --version            print version

Exit codes: 0 healthy, 1 problems found, 2 error.
`

type opts struct {
	ns, ctx, kubeconfig, output string
	noColor                     bool
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
		fmt.Fprintln(stdout, "kubectl-why", version)
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

	c := kube.Client{R: r, Namespace: o.ns}
	reports, err := collect(ctx, c, o.targets, now())
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	if o.output == "json" {
		if err := render.JSON(stdout, reports); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 2
		}
	} else {
		render.Text(stdout, render.Style{Color: !o.noColor && colorOK(stdout)}, reports)
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
	if o.output != "text" && o.output != "json" {
		return o, fmt.Errorf("--output must be text or json, got %q", o.output)
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
				prs = append(prs, diagnose.Pod(p, events["Pod/"+p.Metadata.Name], now))
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
		r := diagnose.Pod(p, events["Pod/"+p.Metadata.Name], now)
		if r.Worst() >= diagnose.Warn || (r.Worst() == diagnose.Info && r.Restarts > 0) {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		ns := c.Namespace
		if ns == "" {
			ns = "current namespace"
		}
		out = append(out, diagnose.Report{
			Kind: "Namespace", Name: ns, Status: "Healthy",
			Ready: fmt.Sprintf("%d pods", len(pods)), Age: "-",
			Findings: []diagnose.Finding{{Severity: diagnose.OK, Title: fmt.Sprintf("All %d pods are healthy", len(pods))}},
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
