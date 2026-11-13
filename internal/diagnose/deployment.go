package diagnose

import (
	"fmt"
	"strings"
	"time"

	"github.com/Sakuratix/kubectl-why/internal/kube"
)

// Deployment summarises rollout state and attaches the pod reports.
// Healthy pods are folded into one line so the output stays short.
func Deployment(d kube.Deployment, pods []Report, now time.Time) Report {
	want := 1
	if d.Spec.Replicas != nil {
		want = *d.Spec.Replicas
	}
	r := Report{
		Kind:      "Deployment",
		Namespace: d.Metadata.Namespace,
		Name:      d.Metadata.Name,
		Ready:     fmt.Sprintf("%d/%d", d.Status.ReadyReplicas, want),
		Age:       Human(now.Sub(d.Metadata.CreationTimestamp)),
	}
	r.Status = "Available"
	for _, c := range d.Status.Conditions {
		switch {
		case c.Type == "Progressing" && c.Reason == "ProgressDeadlineExceeded":
			r.Status = "RolloutStuck"
			r.Findings = append(r.Findings, Finding{
				Severity: Crit,
				Title:    "Rollout is stuck (progressDeadlineSeconds exceeded)",
				Detail:   c.Message,
				Hints: []string{
					"New pods never became Ready, see below. Fix them or roll back: `kubectl rollout undo deployment/" + d.Metadata.Name + "`.",
				},
			})
		case c.Type == "ReplicaFailure" && c.Status == "True":
			f := Finding{Severity: Crit, Title: "ReplicaSet can't create pods (" + c.Reason + ")", Detail: c.Message}
			if strings.Contains(strings.ToLower(c.Message), "exceeded quota") {
				f.Title = "ResourceQuota blocks new pods"
				f.Hints = []string{"Check `kubectl describe resourcequota -n " + orDash(d.Metadata.Namespace) + "`: raise the quota or lower requests/limits."}
			}
			r.Findings = append(r.Findings, f)
		case c.Type == "Available" && c.Status == "False":
			if r.Status == "Available" {
				r.Status = "Unavailable"
			}
		}
	}
	if want == 0 {
		r.Findings = append(r.Findings, Finding{Severity: Info, Title: "Scaled to zero replicas"})
	} else if d.Status.ReadyReplicas < want && len(r.Findings) == 0 {
		r.Findings = append(r.Findings, Finding{
			Severity: Warn,
			Title:    fmt.Sprintf("Only %d of %d replicas Ready", d.Status.ReadyReplicas, want),
		})
	}

	healthy := 0
	for _, p := range pods {
		r.Restarts += p.Restarts
		if p.Worst() <= Info && p.Restarts == 0 {
			healthy++
			continue
		}
		r.Children = append(r.Children, p)
	}
	if len(r.Findings) == 0 && len(r.Children) == 0 {
		r.Findings = []Finding{{Severity: OK, Title: fmt.Sprintf("Healthy: %d/%d replicas Ready", d.Status.ReadyReplicas, want)}}
	} else if healthy > 0 {
		r.Findings = append(r.Findings, Finding{Severity: OK, Title: fmt.Sprintf("%d pod(s) healthy, not shown", healthy)})
	}
	return r
}
