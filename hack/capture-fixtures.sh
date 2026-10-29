#!/usr/bin/env bash
# Captures the objects around each InferenceService in a namespace as test fixtures
# (docs/mvp.md section 7: unit tests use objects captured from real clusters).
#
# Usage: hack/capture-fixtures.sh <namespace> <output-dir> [name ...]
set -euo pipefail

ns=${1:?namespace}
out=${2:?output directory}
shift 2
names=("$@")
if [ ${#names[@]} -eq 0 ]; then
  mapfile -t names < <(kubectl get isvc -n "$ns" -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}')
fi

# Drop fields that only add noise and change on every capture.
clean='del(.metadata.managedFields) | del(.items[]?.metadata.managedFields)
       | del(.metadata.annotations["kubectl.kubernetes.io/last-applied-configuration"])
       | del(.items[]?.metadata.annotations["kubectl.kubernetes.io/last-applied-configuration"])'
command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }

for name in "${names[@]}"; do
  dir="$out/$name"
  mkdir -p "$dir"
  sel="serving.kserve.io/inferenceservice=$name"
  kubectl get isvc "$name" -n "$ns" -o json | jq "$clean" > "$dir/inferenceservice.json"
  kubectl get pods,deployments,horizontalpodautoscalers,services,ingresses -n "$ns" -l "$sel" -o json | jq "$clean" > "$dir/objects.json"
  # Ingresses and HTTPRoutes are matched by owner reference, so capture every one in the namespace.
  kubectl get ingresses -n "$ns" -o json | jq "$clean" > "$dir/ingresses.json"
  kubectl get httproutes.gateway.networking.k8s.io -n "$ns" -o json 2>/dev/null | jq "$clean" > "$dir/httproutes.json" || echo '{"items":[]}' > "$dir/httproutes.json"
  echo "captured $name"
done

kubectl get ingressclasses -o json | jq "$clean" > "$out/ingressclasses.json"
kubectl get clusterservingruntimes -o json | jq "$clean" > "$out/clusterservingruntimes.json"
kubectl get gateways.gateway.networking.k8s.io -A -o json 2>/dev/null | jq "$clean" > "$out/gateways.json" || echo '{"items":[]}' > "$out/gateways.json"
