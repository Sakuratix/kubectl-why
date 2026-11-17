package diagnose

import (
	"strings"
	"testing"
	"time"

	"github.com/Sakuratix/kubectl-why/internal/kube"
)

func TestHuman(t *testing.T) {
	for d, want := range map[time.Duration]string{
		-time.Second:         "0s",
		42 * time.Second:     "42s",
		17 * time.Minute:     "17m",
		5 * time.Hour:        "5h",
		47 * time.Hour:       "47h",
		72 * time.Hour:       "3d",
		400 * 24 * time.Hour: "400d",
	} {
		if got := Human(d); got != want {
			t.Errorf("Human(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestScheduleHints(t *testing.T) {
	p := kube.Pod{Spec: kube.PodSpec{Containers: []kube.Container{{Name: "app", Resources: kube.ResourceRequirements{Requests: map[string]string{"memory": "2Gi"}}}}}}
	cases := map[string]string{
		"3 Insufficient memory":                               "app=2Gi",
		"1 node(s) had untolerated taint {dedicated: gpu}":    "toleration",
		"2 node(s) didn't match Pod's node affinity/selector": "nodeSelector",
		"1 pod has unbound immediate PersistentVolumeClaims":  "PersistentVolumeClaim",
		"1 node(s) had volume node affinity conflict":         "zone",
		"3 Too many pods":              "max-pods",
		"1 node(s) were unschedulable": "uncordon",
	}
	for clause, want := range cases {
		if h := scheduleHint(clause, p); !strings.Contains(h, want) {
			t.Errorf("scheduleHint(%q) = %q, want it to mention %q", clause, h, want)
		}
	}
	if h := scheduleHint("something new", p); h != "" {
		t.Errorf("unknown clause should give no hint, got %q", h)
	}
}

func TestExitCodeExplained(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p := kube.Pod{
		Metadata: kube.ObjectMeta{Name: "x", CreationTimestamp: now.Add(-time.Hour)},
		Spec:     kube.PodSpec{Containers: []kube.Container{{Name: "app"}}},
		Status: kube.PodStatus{Phase: "Running", ContainerStatuses: []kube.ContainerStatus{{
			Name: "app", RestartCount: 3,
			State: kube.ContainerState{Waiting: &struct {
				Reason  string `json:"reason"`
				Message string `json:"message,omitempty"`
			}{Reason: "CrashLoopBackOff"}},
			LastState: kube.ContainerState{Terminated: &kube.Terminated{ExitCode: 127, Reason: "Error", FinishedAt: now.Add(-time.Minute)}},
		}}},
	}
	r := Pod(p, nil, now)
	if r.Worst() != Crit {
		t.Fatalf("want critical, got %v", r.Worst())
	}
	f := r.Findings[0]
	if !strings.Contains(f.Detail, "not found in the image") || !strings.Contains(strings.Join(f.Hints, " "), "command/args") {
		t.Errorf("exit 127 not explained: %+v", f)
	}
}

func TestRestartedButRunningIsInfo(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p := kube.Pod{
		Metadata: kube.ObjectMeta{Name: "x", CreationTimestamp: now.Add(-time.Hour)},
		Spec:     kube.PodSpec{Containers: []kube.Container{{Name: "app"}}},
		Status: kube.PodStatus{Phase: "Running", ContainerStatuses: []kube.ContainerStatus{{
			Name: "app", Ready: true, RestartCount: 1,
			State: kube.ContainerState{Running: &struct {
				StartedAt time.Time `json:"startedAt"`
			}{StartedAt: now.Add(-30 * time.Minute)}},
			LastState: kube.ContainerState{Terminated: &kube.Terminated{ExitCode: 1, Reason: "Error", FinishedAt: now.Add(-31 * time.Minute)}},
		}}},
	}
	r := Pod(p, nil, now)
	if r.Worst() != Info || !strings.Contains(r.Findings[0].Title, "Restarted 1 time") {
		t.Errorf("unexpected: %+v", r.Findings)
	}
}

func TestDeploymentScaledToZero(t *testing.T) {
	zero := 0
	var d kube.Deployment
	d.Metadata.Name = "api"
	d.Spec.Replicas = &zero
	r := Deployment(d, nil, time.Now())
	if r.Worst() != Info || r.Findings[0].Title != "Scaled to zero replicas" {
		t.Errorf("unexpected: %+v", r.Findings)
	}
}

func TestStartProblem(t *testing.T) {
	cases := map[string]string{
		`failed to create containerd task: failed to create shim task: OCI runtime create failed: runc create failed: unable to start container process: exec: "gunicorn": executable file not found in $PATH: unknown`: `"gunicorn": executable file not found in $PATH`,
		`OCI runtime create failed: runc create failed: unable to start container process: error during container init: open /dev/null: permission denied: unknown`:                                                     `error during container init: open /dev/null: permission denied`,
		"":                           "the runtime gave no reason",
		"something odd\nsecond line": "something odd",
	}
	for in, want := range cases {
		if got := startProblem(in); got != want {
			t.Errorf("startProblem(%q) = %q, want %q", in, got, want)
		}
	}
}
