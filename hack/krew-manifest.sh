#!/usr/bin/env bash
# Writes a krew plugin manifest (why.yaml) for a release from GoReleaser's checksums.
#   hack/krew-manifest.sh v0.1.0 dist/checksums.txt > dist/why.yaml
set -euo pipefail
tag=$1 sums=$2
base="https://github.com/Sakuratix/kubectl-why/releases/download/$tag"
sha() { awk -v f="$1" '$2==f {print $1}' "$sums"; }

cat <<YAML
apiVersion: krew.googlecontainertools.github.com/v1alpha2
kind: Plugin
metadata:
  name: why
spec:
  version: $tag
  homepage: https://github.com/Sakuratix/kubectl-why
  shortDescription: Explain in plain words why a pod or deployment is unhealthy
  description: |
    Reads a pod's status, container states, probes and events and says what is
    actually wrong (OOMKilled, bad image tag, missing secret, failing probe,
    unschedulable, stuck rollout, ...) and what to try next.
  platforms:
YAML
for os in linux darwin windows; do
  for arch in amd64 arm64; do
    ext=tar.gz bin=kubectl-why
    [[ $os == windows ]] && ext=zip bin=kubectl-why.exe
    file="kubectl-why_${os}_${arch}.${ext}"
    s=$(sha "$file"); [[ -n $s ]] || { echo "no checksum for $file" >&2; exit 1; }
    cat <<YAML
    - selector:
        matchLabels: {os: $os, arch: $arch}
      uri: $base/$file
      sha256: $s
      bin: $bin
YAML
  done
done
