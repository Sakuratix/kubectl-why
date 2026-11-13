// Package diagnose turns raw pod/deployment state and events into findings a
// human can act on: what is wrong, the evidence, and what to try next.
package diagnose

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Sakuratix/kubectl-why/internal/kube"
)

type Severity int

const (
	OK Severity = iota
	Info
	Warn
	Crit
)

func (s Severity) String() string {
	return [...]string{"ok", "info", "warning", "critical"}[s]
}

func (s Severity) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

type Finding struct {
	Severity  Severity `json:"severity"`
	Container string   `json:"container,omitempty"`
	Title     string   `json:"title"`
	Detail    string   `json:"detail,omitempty"`
	Evidence  []string `json:"evidence,omitempty"`
	Hints     []string `json:"hints,omitempty"`
}

type Report struct {
	Kind      string    `json:"kind"`
	Namespace string    `json:"namespace"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	Ready     string    `json:"ready"`
	Restarts  int       `json:"restarts"`
	Age       string    `json:"age"`
	Node      string    `json:"node,omitempty"`
	Findings  []Finding `json:"findings"`
	Warnings  []string  `json:"recentWarnings,omitempty"`
	Children  []Report  `json:"pods,omitempty"`
}

// Worst returns the highest severity in the report and its children.
func (r Report) Worst() Severity {
	w := OK
	for _, f := range r.Findings {
		if f.Severity > w {
			w = f.Severity
		}
	}
	for _, c := range r.Children {
		if s := c.Worst(); s > w {
			w = s
		}
	}
	return w
}

// Pod diagnoses a single pod given its events (oldest first).
func Pod(p kube.Pod, events []kube.Event, now time.Time) Report {
	r := Report{
		Kind:      "Pod",
		Namespace: p.Metadata.Namespace,
		Name:      p.Metadata.Name,
		Status:    podStatus(p),
		Ready:     readyCount(p),
		Restarts:  restarts(p),
		Age:       Human(now.Sub(p.Metadata.CreationTimestamp)),
		Node:      p.Spec.NodeName,
	}
	d := &podDiag{p: p, ev: events, now: now}
	d.run()
	r.Findings = d.out
	if len(r.Findings) == 0 {
		r.Findings = []Finding{{Severity: OK, Title: healthyTitle(p)}}
	}
	sort.SliceStable(r.Findings, func(i, j int) bool { return r.Findings[i].Severity > r.Findings[j].Severity })
	r.Warnings = recentWarnings(events, d.used, now, 4)
	return r
}

type podDiag struct {
	p    kube.Pod
	ev   []kube.Event
	now  time.Time
	out  []Finding
	used map[string]bool // event reasons already explained by a finding
}

func (d *podDiag) add(f Finding) { d.out = append(d.out, f) }

func (d *podDiag) markUsed(reasons ...string) {
	if d.used == nil {
		d.used = map[string]bool{}
	}
	for _, r := range reasons {
		d.used[r] = true
	}
}

func (d *podDiag) run() {
	p := d.p
	if p.Metadata.DeletionTimestamp != nil {
		d.terminating()
	}
	if p.Status.Reason == "Evicted" {
		d.add(Finding{
			Severity: Crit,
			Title:    "Evicted by the kubelet",
			Detail:   p.Status.Message,
			Hints: []string{
				"The node ran low on a resource (usually memory or ephemeral disk) and picked this pod to free it.",
				"Set memory requests close to real usage so the pod is not the first candidate, and check `kubectl describe node " + orDash(p.Spec.NodeName) + "` for pressure conditions.",
				"Evicted pods are not cleaned up automatically: `kubectl delete pod " + p.Metadata.Name + "` once you have read this.",
			},
		})
		d.markUsed("Evicted")
		return
	}
	if c := cond(p, "PodScheduled"); c != nil && c.Status == "False" {
		d.unschedulable(*c)
		return
	}
	for _, cs := range p.Status.InitContainerStatuses {
		d.container(cs, true)
	}
	for _, cs := range p.Status.ContainerStatuses {
		d.container(cs, false)
	}
	if p.Status.Phase == "Pending" && len(p.Status.ContainerStatuses) == 0 && p.Spec.NodeName != "" {
		d.sandbox()
	}
	if p.Status.Phase == "Failed" && len(d.out) == 0 {
		d.add(Finding{Severity: Crit, Title: "Pod failed: " + orDash(p.Status.Reason), Detail: p.Status.Message})
	}
}

func (d *podDiag) terminating() {
	p := d.p
	since := d.now.Sub(*p.Metadata.DeletionTimestamp)
	if since < 2*time.Minute {
		d.add(Finding{Severity: Info, Title: "Terminating (" + Human(since) + ")"})
		return
	}
	f := Finding{
		Severity: Crit,
		Title:    "Stuck in Terminating for " + Human(since),
	}
	if len(p.Metadata.Finalizers) > 0 {
		f.Detail = "Blocked by finalizers: " + strings.Join(p.Metadata.Finalizers, ", ")
		f.Hints = append(f.Hints, "A controller is supposed to remove these finalizers. Check that it is running before removing them by hand.")
	}
	if p.Spec.NodeName != "" {
		f.Hints = append(f.Hints, "If node "+p.Spec.NodeName+" is NotReady, the kubelet can't confirm the shutdown: `kubectl get node "+p.Spec.NodeName+"`.")
	}
	f.Hints = append(f.Hints, "Last resort: `kubectl delete pod "+p.Metadata.Name+" --grace-period=0 --force` (the process may still be running on the node).")
	d.add(f)
}

var clauseRe = regexp.MustCompile(`^\s*(\d+)\s+(.+?)\.?\s*$`)

func (d *podDiag) unschedulable(c kube.Condition) {
	msg := c.Message
	if msg == "" {
		if e := d.lastEvent("FailedScheduling"); e != nil {
			msg = e.Message
		}
	}
	d.markUsed("FailedScheduling")
	f := Finding{Severity: Crit, Title: "Can't be scheduled on any node"}
	head, rest, ok := strings.Cut(msg, ": ")
	if ok && strings.Contains(head, "nodes are available") {
		f.Detail = strings.TrimSuffix(head, ":")
		rest, _, _ = strings.Cut(rest, " preemption:")
		for _, cl := range strings.Split(rest, ", ") {
			cl = strings.TrimSpace(strings.TrimSuffix(cl, "."))
			if cl == "" {
				continue
			}
			f.Evidence = append(f.Evidence, cl)
			if h := scheduleHint(cl, d.p); h != "" && !contains(f.Hints, h) {
				f.Hints = append(f.Hints, h)
			}
		}
	} else {
		f.Detail = msg
	}
	if len(f.Hints) == 0 {
		f.Hints = []string{"Run `kubectl describe pod " + d.p.Metadata.Name + "` and compare the pod's requests, nodeSelector and tolerations with `kubectl describe nodes`."}
	}
	d.add(f)
}

func scheduleHint(clause string, p kube.Pod) string {
	m := clauseRe.FindStringSubmatch(clause)
	text := clause
	if m != nil {
		text = m[2]
	}
	l := strings.ToLower(text)
	switch {
	case strings.Contains(l, "insufficient cpu"), strings.Contains(l, "insufficient memory"):
		res := "cpu"
		if strings.Contains(l, "memory") {
			res = "memory"
		}
		return fmt.Sprintf("No node has enough free %s for the requests (%s). Lower them, free capacity, or add nodes.", res, requests(p, res))
	case strings.Contains(l, "untolerated taint"), strings.Contains(l, "had taint"):
		return "Some nodes are tainted. Add a matching toleration or schedule elsewhere: `kubectl get nodes -o custom-columns=NAME:.metadata.name,TAINTS:.spec.taints`."
	case strings.Contains(l, "volume node affinity conflict"):
		return "The pod's volume lives in a zone where no suitable node is. Schedule in the volume's zone or recreate the volume."
	case strings.Contains(l, "node affinity"), strings.Contains(l, "node selector"):
		return "nodeSelector / nodeAffinity matches no node. Compare with `kubectl get nodes --show-labels`."
	case strings.Contains(l, "unbound immediate persistentvolumeclaims"), strings.Contains(l, "persistentvolumeclaim"):
		return "A PersistentVolumeClaim is not bound. Check `kubectl get pvc` and that its StorageClass exists and can provision."
	case strings.Contains(l, "too many pods"):
		return "Nodes hit their max-pods limit. Add nodes or raise the kubelet's maxPods."
	case strings.Contains(l, "didn't have free ports"), strings.Contains(l, "free ports"):
		return "A hostPort is already taken on every candidate node."
	case strings.Contains(l, "unschedulable"):
		return "Nodes are cordoned. `kubectl uncordon <node>` when maintenance is done."
	}
	return ""
}

func requests(p kube.Pod, res string) string {
	var parts []string
	for _, c := range p.Spec.Containers {
		if v := c.Resources.Requests[res]; v != "" {
			parts = append(parts, c.Name+"="+v)
		}
	}
	if len(parts) == 0 {
		return "none set"
	}
	return strings.Join(parts, ", ")
}

var exitCodes = map[int]string{
	1:   "the application exited with an error",
	2:   "misuse of a shell builtin or bad arguments",
	126: "the command is not executable",
	127: "the command was not found in the image",
	128: "invalid exit argument",
	134: "aborted (SIGABRT)",
	137: "killed with SIGKILL (out of memory, or killed after failing a liveness probe)",
	139: "segmentation fault (SIGSEGV)",
	143: "stopped with SIGTERM",
}

func (d *podDiag) container(cs kube.ContainerStatus, init bool) {
	label := cs.Name
	if init {
		label = "init:" + cs.Name
	}
	spec := d.spec(cs.Name, init)

	if w := cs.State.Waiting; w != nil {
		switch w.Reason {
		case "ImagePullBackOff", "ErrImagePull", "InvalidImageName", "ErrImageNeverPull":
			d.imagePull(cs, label, w.Message)
			return
		case "CreateContainerConfigError", "CreateContainerError":
			d.configError(label, w.Reason, w.Message)
			return
		case "CrashLoopBackOff", "RunContainerError":
			d.crash(cs, label, spec, true)
			return
		case "ContainerCreating", "PodInitializing":
			e := d.lastEventMatching("FailedAttachVolume", "Multi-Attach")
			if e == nil {
				e = d.lastEvent("FailedMount", "FailedAttachVolume")
			}
			if e != nil {
				d.markUsed("FailedMount", "FailedAttachVolume")
				d.add(Finding{
					Severity: Crit, Container: label,
					Title:  "Stuck creating: a volume can't be mounted",
					Detail: e.Message,
					Hints:  mountHints(e.Message),
				})
				return
			}
			if e := d.lastEvent("FailedCreatePodSandBox"); e != nil {
				d.sandbox()
				return
			}
		}
	}
	if t := cs.State.Terminated; t != nil && init && t.ExitCode != 0 {
		d.crash(cs, label, spec, false)
		return
	}
	if init {
		if cs.State.Running != nil {
			d.add(Finding{
				Severity: Warn, Container: label,
				Title: "Init container still running (" + Human(d.now.Sub(cs.State.Running.StartedAt)) + ")",
				Hints: []string{"Main containers wait for it. It is usually waiting for a dependency: `kubectl logs " + d.p.Metadata.Name + " -c " + cs.Name + "`."},
			})
		}
		return
	}
	if cs.State.Running != nil && !cs.Ready {
		d.notReady(cs, label, spec)
		return
	}
	if cs.RestartCount > 0 && cs.LastState.Terminated != nil {
		t := cs.LastState.Terminated
		f := Finding{
			Severity:  Info,
			Container: label,
			Title:     fmt.Sprintf("Restarted %d time(s), last exit: %s", cs.RestartCount, exitSummary(*t)),
			Detail:    "Running now; the last restart was " + Human(d.now.Sub(t.FinishedAt)) + " ago.",
		}
		if t.Reason == "OOMKilled" {
			f.Severity = Warn
			f.Hints = []string{"It survives for now, but it already hit its memory limit (" + orDash(spec.Resources.Limits["memory"]) + ") once."}
		}
		d.add(f)
	}
}

func (d *podDiag) spec(name string, init bool) kube.Container {
	list := d.p.Spec.Containers
	if init {
		list = d.p.Spec.InitContainers
	}
	for _, c := range list {
		if c.Name == name {
			return c
		}
	}
	return kube.Container{Name: name}
}

func (d *podDiag) imagePull(cs kube.ContainerStatus, label, msg string) {
	d.markUsed("Failed", "BackOff", "ErrImagePull", "ImagePullBackOff")
	if e := d.lastEventMatching("Failed", "pull"); e != nil {
		msg = e.Message
	}
	f := Finding{
		Severity: Crit, Container: label,
		Title:  "Image can't be pulled: " + cs.Image,
		Detail: msg,
	}
	l := strings.ToLower(msg)
	switch {
	case strings.Contains(l, "not found"), strings.Contains(l, "manifest unknown"), strings.Contains(l, "does not exist"):
		f.Title = "Image or tag doesn't exist: " + cs.Image
		f.Hints = []string{"Check the tag was actually pushed (typo, CI didn't publish, or it was built for another architecture)."}
	case strings.Contains(l, "unauthorized"), strings.Contains(l, "denied"), strings.Contains(l, "authentication required"), strings.Contains(l, "403"):
		f.Title = "Registry refused access to " + cs.Image
		f.Hints = []string{"The registry is private. Add imagePullSecrets to the pod or its ServiceAccount, and check the secret is in namespace " + orDash(d.p.Metadata.Namespace) + "."}
	case strings.Contains(l, "no such host"), strings.Contains(l, "i/o timeout"), strings.Contains(l, "connection refused"), strings.Contains(l, "tls"):
		f.Title = "Registry unreachable from the node"
		f.Hints = []string{"Node " + orDash(d.p.Spec.NodeName) + " can't reach the registry: DNS, proxy, firewall or a registry outage."}
	case strings.Contains(l, "toomanyrequests"), strings.Contains(l, "rate limit"):
		f.Title = "Registry rate limit hit"
		f.Hints = []string{"Docker Hub limits anonymous pulls. Authenticate, use a mirror, or move the image to your own registry."}
	default:
		f.Hints = []string{"`kubectl describe pod " + d.p.Metadata.Name + "` shows the full pull error."}
	}
	d.add(f)
}

var missingRe = regexp.MustCompile(`(secret|configmap)s? "([^"]+)" not found`)
var keyRe = regexp.MustCompile(`couldn't find key (\S+) in (Secret|ConfigMap) ([^\s]+)`)

func (d *podDiag) configError(label, reason, msg string) {
	d.markUsed("Failed")
	f := Finding{Severity: Crit, Container: label, Title: "Container can't be created (" + reason + ")", Detail: msg}
	if m := missingRe.FindStringSubmatch(strings.ToLower(msg)); m != nil {
		f.Title = fmt.Sprintf("Missing %s %q", m[1], m[2])
		f.Hints = []string{fmt.Sprintf("Create it in namespace %s, or fix the name it is referenced by: `kubectl get %s -n %s`.", orDash(d.p.Metadata.Namespace), m[1], orDash(d.p.Metadata.Namespace))}
	} else if m := keyRe.FindStringSubmatch(msg); m != nil {
		f.Title = fmt.Sprintf("Key %q missing in %s %s", m[1], m[2], m[3])
		f.Hints = []string{"The object exists but has no such key. Compare with `kubectl get " + strings.ToLower(m[2]) + " " + strings.TrimPrefix(m[3], orDash(d.p.Metadata.Namespace)+"/") + " -o yaml`."}
	}
	d.add(f)
}

func (d *podDiag) crash(cs kube.ContainerStatus, label string, spec kube.Container, looping bool) {
	t := cs.LastState.Terminated
	if t == nil {
		t = cs.State.Terminated
	}
	d.markUsed("BackOff")
	f := Finding{Severity: Crit, Container: label}
	if looping {
		f.Title = fmt.Sprintf("CrashLoopBackOff: restarted %d times", cs.RestartCount)
	} else {
		f.Title = "Init container failed"
	}
	logs := fmt.Sprintf("`kubectl logs %s -c %s --previous`", d.p.Metadata.Name, cs.Name)
	if t == nil {
		f.Hints = []string{"See why the last run ended: " + logs}
		d.add(f)
		return
	}
	f.Evidence = append(f.Evidence, "last exit: "+exitSummary(*t)+", "+Human(d.now.Sub(t.FinishedAt))+" ago")
	if t.Message != "" {
		f.Evidence = append(f.Evidence, "termination message: "+firstLine(t.Message))
	}
	live := d.lastEventMatching("Unhealthy", "Liveness probe failed")
	switch {
	case t.Reason == "OOMKilled":
		limit := spec.Resources.Limits["memory"]
		f.Title = "OOMKilled: went over its memory limit (" + orDash(limit) + ")"
		f.Hints = []string{
			"Raise resources.limits.memory, or find what grows: heap size flags (-Xmx, GOMEMLIMIT, Node --max-old-space-size) must fit under the limit.",
			"Watch usage before the next kill: `kubectl top pod " + d.p.Metadata.Name + " --containers`.",
		}
	case live != nil && (t.ExitCode == 137 || t.ExitCode == 143 || t.ExitCode == 2):
		d.markUsed("Unhealthy", "Killing")
		f.Title = "Killed by its liveness probe"
		f.Evidence = append(f.Evidence, "probe: "+probeString(spec.LivenessProbe), "last failure: "+trimProbeMsg(live.Message))
		f.Hints = []string{
			"The app starts but doesn't answer the probe in time. If startup is slow, add a startupProbe instead of a longer initialDelaySeconds.",
			"Make sure the probe hits a cheap endpoint that doesn't depend on databases or other services.",
		}
	case t.ExitCode == 0 && looping:
		f.Title = "Exits successfully but is restarted (restartPolicy: Always)"
		f.Hints = []string{"The main process finishes instead of staying in the foreground. For one-off work use a Job; for a server check the command doesn't daemonize."}
	default:
		if why, ok := exitCodes[t.ExitCode]; ok {
			f.Detail = "Exit code " + fmt.Sprint(t.ExitCode) + ": " + why + "."
		}
		f.Hints = []string{"The reason is in the logs of the previous run: " + logs}
		if t.ExitCode == 127 || t.ExitCode == 126 {
			f.Hints = append([]string{"Check command/args and that the binary exists in image " + cs.Image + "."}, f.Hints...)
		}
	}
	d.add(f)
}

func (d *podDiag) notReady(cs kube.ContainerStatus, label string, spec kube.Container) {
	if cs.Started != nil && !*cs.Started {
		if e := d.lastEventMatching("Unhealthy", "Startup probe failed"); e != nil {
			d.markUsed("Unhealthy")
			d.add(Finding{
				Severity: Warn, Container: label,
				Title:    "Startup probe is failing",
				Evidence: []string{"probe: " + probeString(spec.StartupProbe), "last failure: " + trimProbeMsg(e.Message)},
				Hints:    []string{"Liveness and readiness don't run until this passes. Raise failureThreshold × periodSeconds if the app just starts slowly."},
			})
			return
		}
	}
	f := Finding{Severity: Warn, Container: label, Title: "Running but not Ready: receives no traffic from Services"}
	if e := d.lastEventMatching("Unhealthy", "Readiness probe failed"); e != nil {
		d.markUsed("Unhealthy")
		f.Title = "Readiness probe failing: no traffic from Services"
		f.Evidence = []string{"probe: " + probeString(spec.ReadinessProbe), "last failure: " + trimProbeMsg(e.Message), fmt.Sprintf("failed %d time(s)", max(1, e.Count))}
		l := strings.ToLower(e.Message)
		switch {
		case strings.Contains(l, "connection refused"):
			f.Hints = []string{"Nothing listens on that port: the app listens on another port or only on 127.0.0.1 (it must bind 0.0.0.0)."}
		case strings.Contains(l, "statuscode: 5"), strings.Contains(l, "status code: 5"):
			f.Hints = []string{"The endpoint answers with 5xx: usually a dependency (DB, cache) is down. Check the app logs."}
		case strings.Contains(l, "statuscode: 404"), strings.Contains(l, "status code: 404"):
			f.Hints = []string{"The probe path doesn't exist in the app. Fix readinessProbe.httpGet.path."}
		case strings.Contains(l, "timeout"), strings.Contains(l, "deadline exceeded"):
			f.Hints = []string{"The endpoint is too slow. Raise timeoutSeconds or make the check cheaper."}
		default:
			f.Hints = []string{"Run the probe by hand: `kubectl exec " + d.p.Metadata.Name + " -c " + cs.Name + " -- wget -qO- localhost:<port><path>`."}
		}
	} else if spec.ReadinessProbe == nil {
		f.Severity = Info
		f.Title = "Not Ready yet"
	}
	d.add(f)
}

func (d *podDiag) sandbox() {
	if e := d.lastEvent("FailedCreatePodSandBox"); e != nil {
		d.markUsed("FailedCreatePodSandBox")
		d.add(Finding{
			Severity: Crit,
			Title:    "Pod sandbox can't be created (network/runtime)",
			Detail:   firstLine(e.Message),
			Hints:    []string{"Usually the CNI plugin on node " + orDash(d.p.Spec.NodeName) + ": check its pods in kube-system and free IPs in the pod CIDR."},
		})
	}
}

func mountHints(msg string) []string {
	l := strings.ToLower(msg)
	switch {
	case strings.Contains(l, "not found"):
		return []string{"A referenced Secret/ConfigMap/PVC doesn't exist in this namespace."}
	case strings.Contains(l, "multi-attach"):
		return []string{"A ReadWriteOnce volume is still attached to another node (often the old pod during a rollout). Use strategy Recreate or wait for the old pod to go."}
	case strings.Contains(l, "timed out"):
		return []string{"The storage driver didn't attach the volume in time. Check the CSI driver pods and the VolumeAttachment objects."}
	}
	return []string{"`kubectl describe pod` and `kubectl get events` show the storage driver's full error."}
}

// ---------------------------------------------------------------------------

func (d *podDiag) lastEvent(reasons ...string) *kube.Event {
	for i := len(d.ev) - 1; i >= 0; i-- {
		for _, r := range reasons {
			if d.ev[i].Reason == r {
				return &d.ev[i]
			}
		}
	}
	return nil
}

func (d *podDiag) lastEventMatching(reason, substr string) *kube.Event {
	for i := len(d.ev) - 1; i >= 0; i-- {
		if d.ev[i].Reason == reason && strings.Contains(strings.ToLower(d.ev[i].Message), strings.ToLower(substr)) {
			return &d.ev[i]
		}
	}
	return nil
}

func recentWarnings(ev []kube.Event, used map[string]bool, now time.Time, n int) []string {
	var out []string
	for i := len(ev) - 1; i >= 0 && len(out) < n; i-- {
		e := ev[i]
		if e.Type != "Warning" || used[e.Reason] {
			continue
		}
		line := fmt.Sprintf("%s ago  %s: %s", Human(now.Sub(e.When())), e.Reason, firstLine(e.Message))
		if e.Count > 1 {
			line += fmt.Sprintf(" (x%d)", e.Count)
		}
		out = append(out, line)
	}
	return out
}

func cond(p kube.Pod, t string) *kube.Condition {
	for i := range p.Status.Conditions {
		if p.Status.Conditions[i].Type == t {
			return &p.Status.Conditions[i]
		}
	}
	return nil
}

func podStatus(p kube.Pod) string {
	if p.Metadata.DeletionTimestamp != nil {
		return "Terminating"
	}
	if p.Status.Reason != "" {
		return p.Status.Reason
	}
	for _, cs := range p.Status.InitContainerStatuses {
		if w := cs.State.Waiting; w != nil && w.Reason != "PodInitializing" {
			return "Init:" + w.Reason
		}
		if t := cs.State.Terminated; t != nil && t.ExitCode != 0 {
			return "Init:Error"
		}
	}
	for _, cs := range p.Status.ContainerStatuses {
		if w := cs.State.Waiting; w != nil {
			return w.Reason
		}
		if t := cs.State.Terminated; t != nil && p.Status.Phase != "Succeeded" {
			return t.Reason
		}
	}
	return p.Status.Phase
}

func readyCount(p kube.Pod) string {
	n := 0
	for _, cs := range p.Status.ContainerStatuses {
		if cs.Ready {
			n++
		}
	}
	return fmt.Sprintf("%d/%d", n, len(p.Spec.Containers))
}

func restarts(p kube.Pod) int {
	n := 0
	for _, cs := range p.Status.ContainerStatuses {
		n += cs.RestartCount
	}
	return n
}

func healthyTitle(p kube.Pod) string {
	if p.Status.Phase == "Succeeded" {
		return "Completed successfully"
	}
	return "Healthy: all containers are running and Ready"
}

func exitSummary(t kube.Terminated) string {
	s := fmt.Sprintf("exit code %d", t.ExitCode)
	if t.Reason != "" {
		s = t.Reason + ", " + s
	}
	return s
}

func probeString(p *kube.Probe) string {
	if p == nil {
		return "-"
	}
	var s string
	switch {
	case p.HTTPGet != nil:
		s = fmt.Sprintf("GET %s on port %v", p.HTTPGet.Path, p.HTTPGet.Port)
	case p.TCPSocket != nil:
		s = fmt.Sprintf("TCP port %v", p.TCPSocket.Port)
	case p.Exec != nil:
		s = "exec " + strings.Join(p.Exec.Command, " ")
	default:
		s = "grpc/other"
	}
	period, fails := p.PeriodSeconds, p.FailureThreshold
	if period == 0 {
		period = 10
	}
	if fails == 0 {
		fails = 3
	}
	return fmt.Sprintf("%s, every %ds, %d failures allowed, initial delay %ds", s, period, fails, p.InitialDelaySeconds)
}

func trimProbeMsg(m string) string {
	for _, p := range []string{"Liveness probe failed: ", "Readiness probe failed: ", "Startup probe failed: "} {
		m = strings.TrimPrefix(m, p)
	}
	return firstLine(m)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 220 {
		s = s[:217] + "..."
	}
	return s
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// Human formats a duration the way kubectl does: 45s, 12m, 3h, 5d.
func Human(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
