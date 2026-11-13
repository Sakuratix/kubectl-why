#!/usr/bin/env bash
# End-to-end test against a real cluster (kind in CI).
# Deploys broken pods and checks kubectl-why names the right cause for each.
set -euo pipefail

BIN=${BIN:-./kubectl-why}
NS=why-e2e
here=$(cd "$(dirname "$0")" && pwd)

kubectl apply -f "$here/manifests/broken.yaml"

echo "waiting for pods to reach their broken states..."
wait_for() { # pod jsonpath expected
  for _ in $(seq 1 90); do
    got=$(kubectl -n "$NS" get pod "$1" -o jsonpath="$2" 2>/dev/null || true)
    [[ "$got" == *"$3"* ]] && return 0
    sleep 2
  done
  echo "timeout waiting for $1: $2 ~ $3 (got '$got')"; kubectl -n "$NS" describe pod "$1"; return 1
}
wait_for oom      '{.status.containerStatuses[0].lastState.terminated.reason}' OOMKilled
wait_for crash    '{.status.containerStatuses[0].lastState.terminated.exitCode}' 3
wait_for badimage '{.status.containerStatuses[0].state.waiting.reason}' Image
wait_for nosecret '{.status.containerStatuses[0].state.waiting.reason}' CreateContainerConfigError
wait_for notready '{.status.containerStatuses[0].state.running.startedAt}' T
wait_for pending  '{.status.conditions[0].reason}' Unschedulable
wait_for healthy  '{.status.containerStatuses[0].ready}' true
sleep 8 # let a few readiness failures and the deployment deadline accumulate
kubectl -n "$NS" wait deploy/api --for=condition=Progressing=False --timeout=120s || true

fail=0
check() { # target expected-exit pattern...
  local target=$1 want=$2; shift 2
  set +e; out=$("$BIN" -n "$NS" --no-color "$target" 2>&1); code=$?; set -e
  echo "----- kubectl why $target (exit $code)"; echo "$out"
  [[ $code == "$want" ]] || { echo "FAIL: exit $code, want $want"; fail=1; }
  for p in "$@"; do
    grep -qF -- "$p" <<<"$out" || { echo "FAIL: output lacks: $p"; fail=1; }
  done
}

check pod/oom      1 "OOMKilled: went over its memory limit (64Mi)"
check pod/crash    1 "CrashLoopBackOff" "exit code 3" "termination message: connect: connection refused"
check pod/badimage 1 "Image or tag doesn't exist" "this-tag-does-not-exist"
check pod/nosecret 1 'Missing secret "db-credentials"'
check pod/notready 1 "Readiness probe failing" "TCP port 8080" "Nothing listens on that port"
check pod/pending  1 "Can't be scheduled on any node" "Insufficient cpu"
check pod/healthy  0 "Healthy"
check deploy/api   1 "Rollout is stuck" "Image or tag doesn't exist"

set +e; "$BIN" -n "$NS" -o json >/tmp/why.json; set -e
python3 -c 'import json,sys; r=json.load(open("/tmp/why.json")); n=len(r); print("namespace scan found", n, "unhealthy pods"); sys.exit(0 if n>=6 else 1)' || fail=1

exit $fail
