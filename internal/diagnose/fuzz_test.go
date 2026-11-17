package diagnose

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sakuratix/kubectl-why/internal/kube"
)

// FuzzPod feeds arbitrary pod and event JSON to the diagnoser. Real clusters
// send half-filled objects (pods mid-creation, events without timestamps,
// containers without status); none of that may panic.
func FuzzPod(f *testing.F) {
	dirs, _ := filepath.Glob("../../testdata/*")
	for _, d := range dirs {
		pod, err := os.ReadFile(filepath.Join(d, "pod.json"))
		if err != nil {
			continue
		}
		ev, _ := os.ReadFile(filepath.Join(d, "events.json"))
		f.Add(pod, ev)
	}
	f.Add([]byte(`{"status":{"containerStatuses":[{"state":{"terminated":{}}}]}}`), []byte(`{"items":[{}]}`))
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, podJSON, eventsJSON []byte) {
		var p kube.Pod
		if json.Unmarshal(podJSON, &p) != nil {
			return
		}
		var ev kube.List[kube.Event]
		_ = json.Unmarshal(eventsJSON, &ev)
		r := Pod(p, ev.Items, now)
		if r.Kind != "Pod" || len(r.Findings) == 0 {
			t.Fatalf("report without findings: %+v", r)
		}
	})
}

func FuzzStartProblem(f *testing.F) {
	f.Add(`OCI runtime create failed: unable to start container process: exec: "x": executable file not found in $PATH: unknown`)
	f.Add("")
	f.Fuzz(func(t *testing.T, msg string) {
		if startProblem(msg) == "" {
			t.Fatalf("empty explanation for %q", msg)
		}
	})
}
