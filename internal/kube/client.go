package kube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// Runner executes kubectl with the given arguments and returns stdout.
// It is an interface so tests can serve recorded JSON instead of a cluster.
type Runner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

// Kubectl runs the real kubectl binary.
type Kubectl struct {
	Binary string   // defaults to "kubectl"
	Global []string // flags passed to every call: --context, --kubeconfig, ...
}

func (k Kubectl) Run(ctx context.Context, args ...string) ([]byte, error) {
	bin := k.Binary
	if bin == "" {
		bin = "kubectl"
	}
	cmd := exec.CommandContext(ctx, bin, append(append([]string{}, k.Global...), args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		var ee *exec.Error
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("kubectl not found in PATH: %w", err)
		}
		return nil, errors.New(msg)
	}
	return out.Bytes(), nil
}

// Client fetches typed objects through a Runner.
type Client struct {
	R         Runner
	Namespace string
	// AllNamespaces lists pods and events across the cluster (kubectl -A).
	AllNamespaces bool
}

func (c Client) nsArgs() []string {
	if c.AllNamespaces {
		return []string{"--all-namespaces"}
	}
	if c.Namespace == "" {
		return nil
	}
	return []string{"-n", c.Namespace}
}

func (c Client) get(ctx context.Context, v any, args ...string) error {
	full := append([]string{"get"}, args...)
	full = append(full, c.nsArgs()...)
	full = append(full, "-o", "json")
	b, err := c.R.Run(ctx, full...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("decode kubectl %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func (c Client) Pod(ctx context.Context, name string) (Pod, error) {
	var p Pod
	return p, c.get(ctx, &p, "pod", name)
}

func (c Client) Pods(ctx context.Context, selector map[string]string) ([]Pod, error) {
	args := []string{"pods"}
	if len(selector) > 0 {
		args = append(args, "-l", SelectorString(selector))
	}
	var l List[Pod]
	if err := c.get(ctx, &l, args...); err != nil {
		return nil, err
	}
	return l.Items, nil
}

func (c Client) Deployment(ctx context.Context, name string) (Deployment, error) {
	var d Deployment
	return d, c.get(ctx, &d, "deployment", name)
}

// EventsFor returns events about one object, oldest first.
func (c Client) EventsFor(ctx context.Context, kind, name string) ([]Event, error) {
	var l List[Event]
	sel := fmt.Sprintf("involvedObject.kind=%s,involvedObject.name=%s", kind, name)
	if err := c.get(ctx, &l, "events", "--field-selector", sel); err != nil {
		return nil, err
	}
	sort.SliceStable(l.Items, func(i, j int) bool { return l.Items[i].When().Before(l.Items[j].When()) })
	return l.Items, nil
}

// EventKey is how Events groups events: by namespace, kind and name, so pods
// with the same name in different namespaces never share events.
func EventKey(namespace, kind, name string) string {
	return namespace + "/" + kind + "/" + name
}

// Events returns all events in scope grouped by EventKey, oldest first.
func (c Client) Events(ctx context.Context) (map[string][]Event, error) {
	var l List[Event]
	if err := c.get(ctx, &l, "events"); err != nil {
		return nil, err
	}
	sort.SliceStable(l.Items, func(i, j int) bool { return l.Items[i].When().Before(l.Items[j].When()) })
	out := map[string][]Event{}
	for _, e := range l.Items {
		ns := e.InvolvedObject.Namespace
		if ns == "" {
			ns = e.Metadata.Namespace
		}
		k := EventKey(ns, e.InvolvedObject.Kind, e.InvolvedObject.Name)
		out[k] = append(out[k], e)
	}
	return out, nil
}

// SelectorString renders matchLabels as a stable kubectl -l value.
func SelectorString(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + m[k]
	}
	return strings.Join(parts, ",")
}
