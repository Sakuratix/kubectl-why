package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite golden files")

// fixtureRunner answers kubectl calls with recorded JSON from a testdata dir.
type fixtureRunner struct {
	dir   string
	calls []string
}

func (f *fixtureRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	if len(args) < 2 || args[0] != "get" {
		return nil, fmt.Errorf("unexpected call: %v", args)
	}
	var file string
	switch args[1] {
	case "pod":
		file = "pod.json"
	case "pods":
		file = "pods.json"
	case "deployment":
		file = "deployment.json"
	case "events":
		file = "events.json"
	default:
		return nil, fmt.Errorf("no fixture for %v", args)
	}
	b, err := os.ReadFile(filepath.Join(f.dir, file))
	if os.IsNotExist(err) {
		return nil, fmt.Errorf(`Error from server (NotFound): %s not found`, args[1])
	}
	return b, err
}

func TestGolden(t *testing.T) {
	now = func() time.Time { return time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC) }
	cases := []struct {
		dir  string
		args []string
		code int
		want string // golden file, want.txt when empty
	}{
		{"oomkilled", []string{"pod/api-7f9c6-x2kq9", "-n", "shop"}, 1, ""},
		{"crashloop", []string{"worker-5d8b7-q8vtl"}, 1, ""},
		{"crash-now", []string{"pod/report-6b4d9-7wq2c"}, 1, ""},
		{"liveness", []string{"pod", "api-7f9c6-l9d2s"}, 1, ""},
		{"image-notfound", []string{"pod/api-7f9c6-pp4mz"}, 1, ""},
		{"image-unauthorized", []string{"pod/billing-6c9f-k2m4x"}, 1, ""},
		{"pending", []string{"pod/search-0"}, 1, ""},
		{"missing-secret", []string{"pod/api-7f9c6-hh7wq"}, 1, ""},
		{"readiness", []string{"pod/web-84d6f-zx7rn"}, 1, ""},
		{"healthy", []string{"pod/api-7f9c6-b7c2d"}, 0, ""},
		{"terminating", []string{"pod/redis-0"}, 1, ""},
		{"evicted", []string{"pod/api-7f9c6-9tq5v"}, 1, ""},
		{"failed-mount", []string{"pod/postgres-0"}, 1, ""},
		{"init-failing", []string{"pod/api-7f9c6-m3k8j"}, 1, ""},
		{"deployment", []string{"deploy/api", "-n", "shop"}, 1, ""},
		{"namespace", []string{"-n", "shop"}, 1, ""},
		{"all-namespaces", []string{"-A"}, 1, ""},
	}
	for _, c := range cases {
		if c.want == "" {
			c.want = "want.txt"
		}
		t.Run(c.dir+"/"+c.want, func(t *testing.T) {
			dir := filepath.Join("..", "..", "testdata", c.dir)
			var out, errb bytes.Buffer
			code := run(append(c.args, "--no-color"), &out, &errb, &fixtureRunner{dir: dir})
			if code != c.code {
				t.Fatalf("exit code %d, want %d; stderr: %s", code, c.code, errb.String())
			}
			golden := filepath.Join(dir, c.want)
			if *update {
				if err := os.WriteFile(golden, out.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("missing golden file (run go test ./... -update): %v", err)
			}
			if out.String() != string(want) {
				t.Errorf("output differs from %s\n--- got ---\n%s\n--- want ---\n%s", golden, out.String(), want)
			}
		})
	}
}

func TestJSONOutput(t *testing.T) {
	now = func() time.Time { return time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC) }
	var out bytes.Buffer
	code := run([]string{"pod/api-7f9c6-x2kq9", "-o", "json"}, &out, &bytes.Buffer{}, &fixtureRunner{dir: "../../testdata/oomkilled"})
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{`"severity": "critical"`, `"title": "OOMKilled: went over its memory limit (256Mi)"`, `"container": "app"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("json output lacks %s:\n%s", want, out.String())
		}
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		args    []string
		ns      string
		targets []string
		err     bool
	}{
		{[]string{"deploy", "api", "-n", "prod"}, "prod", []string{"deploy/api"}, false},
		{[]string{"--namespace=prod", "pod/x"}, "prod", []string{"pod/x"}, false},
		{[]string{"x", "y"}, "", []string{"x", "y"}, false},
		{[]string{"-o", "yaml"}, "", nil, true},
		{[]string{"--bogus"}, "", nil, true},
		{[]string{"-A", "pod/x"}, "", nil, true},
		{[]string{"--all-namespaces"}, "", nil, false},
		{[]string{"-n"}, "", nil, true},
	}
	for _, c := range cases {
		o, err := parse(c.args)
		if (err != nil) != c.err {
			t.Errorf("parse(%v) err=%v, want err=%v", c.args, err, c.err)
			continue
		}
		if c.err {
			continue
		}
		if o.ns != c.ns || strings.Join(o.targets, ",") != strings.Join(c.targets, ",") {
			t.Errorf("parse(%v) = ns %q targets %v", c.args, o.ns, o.targets)
		}
	}
}

func TestUnsupportedKindAndKubectlError(t *testing.T) {
	var errb bytes.Buffer
	if code := run([]string{"svc/api"}, &bytes.Buffer{}, &errb, &fixtureRunner{dir: "../../testdata/healthy"}); code != 2 {
		t.Errorf("svc should fail with 2, got %d", code)
	}
	errb.Reset()
	if code := run([]string{"deploy/api"}, &bytes.Buffer{}, &errb, &fixtureRunner{dir: "../../testdata/healthy"}); code != 2 || !strings.Contains(errb.String(), "NotFound") {
		t.Errorf("missing deployment: code %d, stderr %q", code, errb.String())
	}
}

// TestDemo regenerates the colored output used for docs/demo.svg (with -update).
func TestDemo(t *testing.T) {
	if !*update {
		t.Skip("only with -update")
	}
	now = func() time.Time { return time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC) }
	t.Setenv("CLICOLOR_FORCE", "1")
	f, err := os.Create("../../docs/demo.ansi")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	run([]string{"deploy/api", "-n", "shop"}, f, &bytes.Buffer{}, &fixtureRunner{dir: "../../testdata/deployment"})
}

func TestAllNamespacesAsksKubectlForEverything(t *testing.T) {
	f := &fixtureRunner{dir: "../../testdata/all-namespaces"}
	run([]string{"-A", "--no-color"}, &bytes.Buffer{}, &bytes.Buffer{}, f)
	for _, c := range f.calls {
		if !strings.Contains(c, "--all-namespaces") {
			t.Errorf("kubectl %s: missing --all-namespaces", c)
		}
	}
}
