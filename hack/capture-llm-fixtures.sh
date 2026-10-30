#!/usr/bin/env bash
# Captures the objects around each LLMInferenceService in a namespace as test fixtures (docs/mvp.md section 7),
# plus the previous log of a crashed server container, which the root-cause scan is tested against.
#
# Usage: hack/capture-llm-fixtures.sh <namespace> <output-dir> [name ...]
set -euo pipefail

ns=${1:?namespace}
out=${2:?output directory}
shift 2
names=("$@")
if [ ${#names[@]} -eq 0 ]; then
  mapfile -t names < <(kubectl get llminferenceservices -n "$ns" -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}')
fi

clean='del(.metadata.managedFields) | del(.items[]?.metadata.managedFields)
       | del(.metadata.annotations["kubectl.kubernetes.io/last-applied-configuration"])
       | del(.items[]?.metadata.annotations["kubectl.kubernetes.io/last-applied-configuration"])
       | del(.items[]? | select(.kind == "Secret"))'
command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }

for name in "${names[@]}"; do
  dir="$out/$name"
  mkdir -p "$dir"
  sel="app.kubernetes.io/part-of=llminferenceservice,app.kubernetes.io/name=$name"
  kubectl get llminferenceservices "$name" -n "$ns" -o json | jq "$clean" > "$dir/llminferenceservice.json"
  kubectl get pods,deployments,services -n "$ns" -l "$sel" -o json | jq "$clean" > "$dir/objects.json"
  kubectl get httproutes.gateway.networking.k8s.io -n "$ns" -o json 2>/dev/null | jq "$clean" > "$dir/httproutes.json" || echo '{"items":[]}' > "$dir/httproutes.json"
  # The server's previous instance, when it crashed: the input of the root-cause scan.
  pod=$(kubectl get pods -n "$ns" -l "$sel" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
  if [ -n "$pod" ] && kubectl logs -n "$ns" "$pod" -c main --previous --tail=2000 > "$dir/main-previous.log" 2>/dev/null; then
    :
  else
    rm -f "$dir/main-previous.log"
  fi
  echo "captured $name"
done
