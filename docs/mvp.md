# kubectl-isvc — minimum viable product (version 0.1)

Status: **draft for review** · Last updated: 2026-10-29 · Supports KServe v0.16 – v0.21 (lab cluster runs v0.21.0)

## 1. Purpose

`kubectl-isvc` is a kubectl plugin for day-to-day operation of a single **KServe InferenceService** (short name `isvc`).

One InferenceService fans out into many Kubernetes objects: a ServingRuntime, Deployments, ReplicaSets, Pods, an init container that downloads the model, a Service, an Ingress or HTTPRoute, and a HorizontalPodAutoscaler. When a model is not serving, finding the reason means checking each of them by hand. When it is serving, calling it means working out the right address, protocol and request body.

Version 0.1 answers three questions with one command each:

| Question | Command |
|---|---|
| Is my model serving, and if not, why? | `kubectl isvc status <name>` |
| Does it actually answer a request? | `kubectl isvc test <name> -f input.json` |
| What did its containers print? | `kubectl isvc logs <name>` |

### Goal

Acceptance into the official krew plugin index (`kubernetes-sigs/krew-index`).

### Positioning

There is no existing krew plugin for KServe InferenceService (checked 2026-10-29). The closest neighbor is `inferaudit`, a read-only, cluster-wide audit report of model-serving pods.

> inferaudit = cluster-wide audit report; kubectl-isvc = day-to-day operational commands for a single InferenceService.

## 2. Supported environment

| Item | Version 0.1 |
|---|---|
| KServe deployment mode | **Standard mode only** (formerly called RawDeployment) |
| KServe version | **v0.16.0 (released 2025-11-03) through v0.21.0**. See section 2.1 |
| Kubernetes | Whatever the KServe version in use requires (v0.21.0: 1.32 or newer) |
| InferenceService API | `serving.kserve.io/v1beta1` |
| Network exposure | Kubernetes Ingress **or** Gateway API HTTPRoute, both fully supported (section 2.2). Neither is required, because `test` falls back to port-forward |

Knative (Serverless) mode is out of scope for 0.1. If `status` sees the annotation `serving.kserve.io/deploymentMode` with any value other than `Standard` or `RawDeployment`, it prints a clear "not supported in version 0.1" message instead of wrong output.

### 2.1 Version differences the plugin must handle

v0.16 is the oldest supported version. It is the first release where everything `status` relies on is in place:
- the mode is called `Standard`
- the mode is recorded in `status.deploymentMode`
- the selected runtime is recorded in the status
- Gateway API HTTPRoute exposure is available (it arrived in v0.15)

These differences were checked in the KServe source at each release tag:

| Difference | v0.16 – v0.20 | v0.21 |
|---|---|---|
| Name of the mode | `Standard`; the old name `RawDeployment` is still accepted | same |
| Where the mode is recorded | annotation `serving.kserve.io/deploymentMode`, plus `status.deploymentMode` | same |
| Selected runtime in status | `status.clusterServingRuntimeName` / `status.servingRuntimeName` | same |
| Gateway API HTTPRoute exposure | yes | yes |
| Standard-mode canary (`spec.canary[]`) | no | yes |

Rules that follow from the table:
- **Detect fields, not version numbers.** The plugin reads whatever fields exist and never parses the KServe version. Example: it uses `status.deploymentMode`, and falls back to the annotation while a new InferenceService has no status yet.
- **Both mode names.** Treat `Standard` and `RawDeployment` as the same mode, because objects created before an upgrade may still carry the old name.
- **Canary.** Show the canary section only when `spec.canary` exists. Before v0.21 it never does. The older `spec.predictor.canaryTrafficPercent` field belongs to Knative mode, so it is out of scope.
- **Older KServe.** On v0.15 or older, `status.deploymentMode` and the runtime fields are missing. The plugin then still runs, but it prints one warning line: "KServe older than v0.16 is not supported; some fields may be missing".

Standard-mode canary in v0.21 works like this:
- Each item in the `spec.canary[]` field has a `predictor` and a `trafficPercent`.
- Its state is reported in `status.canaryStatuses[]` and in the `CanaryPredictorReady` condition.
- KServe's validation allows `spec.canary` only in Standard mode.
- The traffic is split through HTTPRoutes, so it needs the Gateway API.

### 2.2 Ingress and Gateway API

KServe in Standard mode exposes an InferenceService in one of two ways, depending on its configuration (`kserve.controller.gateway.ingressGateway.enableGatewayApi`):
- **Kubernetes Ingress** (the default): one Ingress per InferenceService.
- **Gateway API**: one or more HTTPRoutes attached to a shared Gateway.

The plugin finds these objects through their owner reference to the InferenceService, so it does not depend on naming conventions. It then reports, in the same terms for both kinds:
- whether the route was accepted by its controller
- whether it points at existing Services
- the address of the Ingress load balancer, or of the Gateway

## 3. Common behavior

### Flags shared by all commands

The standard kubectl connection flags come from `k8s.io/cli-runtime` and behave exactly as they do in kubectl: `-n/--namespace`, `--context`, `--kubeconfig`, `--cluster`, `--user`, `--as`, `--request-timeout`.

Additional flag:

| Flag | Default | Meaning |
|---|---|---|
| `--no-color` | colors on when the output is a terminal | Turn off colored output. The `NO_COLOR` environment variable also turns it off |

### Exit codes

| Code | Meaning |
|---|---|
| 0 | Command succeeded |
| 1 | Command ran, but the result is a failure. Examples: the inference request returned an error, or `status --exit-code` found the InferenceService not Ready |
| 2 | Wrong usage (unknown flag, missing argument, bad input file) |
| 3 | Could not reach the cluster, the InferenceService was not found, or permission was denied |

By default `status` is informational: it exits 0 whatever the InferenceService's state. With `--exit-code`, it exits 1 when the InferenceService is not Ready, so scripts can use it as a health check.

### Permissions

Version 0.1 only needs read access, plus `pods/portforward` for `test`.
- `get` and `list` on: `inferenceservices`, `servingruntimes`, `clusterservingruntimes`, `pods`, `pods/log`, `deployments`, `replicasets`, `services`, `ingresses`, `ingressclasses`, `httproutes`, `gateways`, `events`
- Missing permission for an optional object (for example `gateways`) downgrades that part of the output to "unknown (no permission)" instead of failing the command.
- `create` on `pods/portforward`

The plugin never creates, changes or deletes cluster objects.

## 4. `kubectl isvc status <name>`

A one-screen summary of an InferenceService and, when it is not working, the most likely root cause.

### What it shows

1. **Header**: name, namespace, deployment mode, overall Ready state, and age.
2. **Endpoints**:
   - the external address (`status.url`)
   - the in-cluster address (`status.address.url`)
   - how it is exposed: an Ingress (with its IngressClass), or HTTPRoutes (with their Gateway)
   - whether the external address is actually routable
3. **Model**:
   - model format and storage location (`storageUri`)
   - the selected runtime (`status.clusterServingRuntimeName` or `status.servingRuntimeName`), with the protocols it supports
   - the model state from `status.modelStatus`, including `lastFailureInfo` when present
4. **Components**: one row per component that exists (predictor, transformer, explainer), with:
   - its Ready condition
   - its Deployment's ready replicas compared to the desired count
   - the autoscaler's minimum and maximum
5. **Canary** (only when `spec.canary` is set): for each canary predictor, its traffic percentage and Ready state, plus the traffic left on the stable predictor.
6. **Conditions**: every condition in `status.conditions`, with status, reason and message.
7. **Diagnosis**: shown only when something is wrong (section 4.2).

### 4.2 Diagnosis rules

The plugin follows the chain InferenceService → Deployment → ReplicaSet → Pod → container and reports the first problem it finds. Each rule names the evidence it used.

| Situation | How it is detected | What it prints |
|---|---|---|
| Model download failed | `storage-initializer` init container exited non-zero or is in back-off | Exit code, plus the last log lines of `storage-initializer` |
| Image cannot be pulled | Container waiting reason `ErrImagePull` / `ImagePullBackOff` | The image name and the pull error message |
| Server crashes on start | `kserve-container` restarting (`CrashLoopBackOff`) | Restart count, last termination reason and exit code, and the last log lines |
| Out of memory | Last termination reason `OOMKilled` | The memory limit that was hit |
| Pod cannot be scheduled | Pod `PodScheduled=False` | The scheduler message (for example "Insufficient nvidia.com/gpu") |
| No runtime fits the model | `modelStatus.lastFailureInfo.reason` is `NoSupportingRuntime`, or there is no runtime name | The model format that has no supporting runtime |
| External address not routable (Ingress) | An Ingress exists, but its `ingressClassName` does not match any IngressClass, or it has no load balancer address, **even though `IngressReady=True`** | A warning, and a hint to use `kubectl isvc test` (which falls back to port-forward) |
| External address not routable (Gateway API) | An HTTPRoute's `status.parents[]` condition `Accepted` or `ResolvedRefs` is not True; or its parent Gateway is missing, not `Programmed`, or has no address | Which route and which Gateway, with the condition's reason and message |
| Canary not ready | `CanaryPredictorReady=False` (v0.21 and newer) | The canary predictor's name, and the same diagnosis rules applied to its pods |
| Leftover Ingress | An Ingress owned by the InferenceService whose hosts do not include the external address host. KServe keeps the old Ingress after a switch to Gateway API | A warning with a delete hint. The Ingress does not count against routability, and `test` never uses its address |

Events are shown only when they help. Known noise is hidden. Example: `UpdateFailed` or `InternalError` events whose message contains "the object has been modified" are optimistic-locking retries by the controller, not failures.

### Flags

| Flag | Default | Meaning |
|---|---|---|
| `-o, --output` | human-readable | `json` or `yaml`: a structured summary (not the raw object) for scripts |
| `--events` | off | Also list every recent event for the related objects |
| `-w, --watch` | off | Redraw when the InferenceService or its pods change |
| `--exit-code` | off | Exit 1 when the InferenceService is not Ready (section 3). With `--watch`, the exit code reflects the state when the user stops watching |

### Example output (target design)

```
sklearn-iris   namespace: kserve-test   mode: Standard   READY   age: 57m

Endpoints
  external    http://sklearn-iris-kserve-test.example.com   ⚠ not routable (IngressClass "istio" not found)
  in-cluster  http://sklearn-iris-predictor.kserve-test.svc.cluster.local

Model
  format      sklearn            storage  gs://kfserving-examples/models/sklearn/1.0/model
  runtime     kserve-sklearnserver (ClusterServingRuntime)   protocols: v1, v2
  state       Loaded (UpToDate)

Components
  predictor   Ready   replicas 1/1   autoscaling 1–1 (cpu 80%)

Conditions
  Ready            True
  PredictorReady   True   NewReplicaSetAvailable
  IngressReady     True
  Stopped          False

Hint: the external address is not reachable; `kubectl isvc test sklearn-iris` will use port-forward.
```

### Acceptance criteria

- With the healthy `sklearn-iris` sample, it shows Ready, both addresses, runtime `kserve-sklearnserver` and protocols v1 and v2.
- It flags the external address as not routable on the lab cluster, where IngressClass `istio` does not exist.
- With KServe switched to Gateway API on the lab cluster, it shows the HTTPRoute and the `lab-gateway` Gateway with its address.
- `status --exit-code` exits 0 for a Ready InferenceService and 1 for each broken sample.
- Against KServe v0.16 it shows the same sections, except canary.
- For each deliberately broken sample in section 7, the diagnosis line names the right cause.
- It finishes in under 2 seconds on the lab cluster for a single InferenceService.

## 5. `kubectl isvc test <name> -f <input.json>`

Sends one smoke-test inference request and prints the response. You don't need to know the address or the protocol.

### Choosing the target

1. If a transformer exists, send to the transformer. Otherwise, send to the predictor.
2. If `--url` is given, use that address.
3. Otherwise, try the external address (`status.url`) with a short timeout (2 seconds).
4. If its host name does not resolve from this machine, but the Ingress load balancer or the Gateway has an address, keep the external URL and only send the TCP connection to that address, as `curl --resolve` does. The Host header, the TLS server name and the certificate check then all use the real host name. This tests the real ingress path without any DNS setup. A leftover Ingress (see 4.2) is never used.
5. If that fails too, port-forward to the component's Service (port 80) on a free local port. Close the port-forward when the command ends.

The command always says which route it used ("via port-forward to svc/sklearn-iris-predictor").

### Choosing the protocol

Use the first rule that gives an answer:
1. The `--protocol` flag (`v1` or `v2`)
2. `spec.predictor.model.protocolVersion` on the InferenceService
3. The shape of the input file: a top-level `instances` key means v1, a top-level `inputs` key means v2
4. The first entry in the selected runtime's `protocolVersions`
5. v1

If the server publishes `/openapi.json`, check that the chosen path exists there. If it doesn't, stop with a clear error instead of sending a request that is bound to fail. KServe's Python model servers publish it; others may not, and then this check is skipped.

### Request paths

| Protocol | Method and path |
|---|---|
| v1 | `POST /v1/models/<model>:predict` |
| v2 (Open Inference Protocol) | `POST /v2/models/<model>/infer` |

The model name defaults to the InferenceService name, which is how KServe's built-in runtimes register it. It can be overridden with `--model`.

### Flags

| Flag | Default | Meaning |
|---|---|---|
| `-f, --filename` | required | Request body file. `-` means read from standard input |
| `--protocol` | automatic | `v1` or `v2` |
| `--model` | InferenceService name | Model name used in the request path |
| `--url` | automatic | Send to this base address instead of discovering one |
| `--port-forward` | `auto` | `auto`, `always`, or `never` |
| `-H, --header` | none | Extra request header, repeatable (for example an authorization token) |
| `--timeout` | `30s` | Request timeout |
| `-o, --output` | `pretty` | Output format, one of `pretty`, `summary`, `raw` or `json` (see Output) |

### Output

The user chooses the format with `-o`:

| Format | What is printed | Typical use |
|---|---|---|
| `pretty` (default) | A header line with route, protocol, address, status code and duration, then the response body pretty-printed | Interactive use |
| `summary` | One line, for example `HTTP 200 · 31 ms · via port-forward · predictions: [1, 1]`. Long bodies are shortened | Quick smoke tests, many models in a loop |
| `raw` | Only the response body, byte for byte, with no header | Piping into `jq` or saving to a file |
| `json` | A JavaScript Object Notation (JSON) document with route, protocol, address, status code, duration and the parsed body | Scripts and continuous integration |

Exit code 0 for a 2xx response, 1 otherwise, in every format.

### Acceptance criteria

- `kubectl isvc test sklearn-iris -f examples/iris-input.json` returns `{"predictions":[1,1]}` on the lab cluster through port-forward, with no extra flags.
- A v2 input file (`inputs` key) against the same InferenceService uses `/v2/models/sklearn-iris/infer`.
- A wrong path, or a protocol the runtime doesn't support, gives a clear error before any request is sent.
- With KServe on Gateway API, it reaches the model over HTTPS through the `lab-gateway` address, without port-forward. Verified 2026-10-29: `HTTP 200 · via HTTPRoute sklearn-iris`.
- Each `-o` format prints as described, and `-o raw` output can be piped straight into `jq`.

## 6. `kubectl isvc logs <name>`

Shows logs from the right pods and containers. You don't need to know pod names, labels or container names.

### What it selects

- Pods with the label `serving.kserve.io/inferenceservice=<name>`. Narrow them by component with the label `component=predictor|transformer|explainer`.
- **By default**: the `kserve-container` of every predictor pod, every replica included.
- With `--init`: the `storage-initializer` init container instead. This is the first place to look when the model never loads.

### Flags

| Flag | Default | Meaning |
|---|---|---|
| `-c, --component` | `predictor` | `predictor`, `transformer`, `explainer`, or `all` |
| `--init` | off | Show `storage-initializer` logs instead of the server's |
| `--all-containers` | off | Every container in the selected pods, sidecars included |
| `-f, --follow` | off | Keep streaming. New pods that appear (for example during a rollout) are picked up |
| `--since` | none | Only logs newer than a duration, for example `10m` |
| `--tail` | `100` per container (`-1` for all) | Number of lines from the end |
| `-p, --previous` | off | Logs of the previous, crashed container instance |
| `--prefix` | on when more than one stream | Prefix every line with `[component/pod/container]` |

### Acceptance criteria

- `kubectl isvc logs sklearn-iris` prints the sklearn server's startup lines without being told any pod name.
- `kubectl isvc logs sklearn-iris --init` prints the model download.
- `-f` keeps following across a pod restart.

## 7. Test plan

Unit tests use the fake dynamic client and the fake Kubernetes client from client-go, fed with real objects captured from the lab cluster.

End-to-end checks run against the lab cluster, namespace `kserve-test`, with these samples.

| Sample | Expected result |
|---|---|
| `examples/sklearn-iris.yaml` | Healthy; all three commands succeed |
| `examples/broken/bad-storage.yaml` | `status` diagnoses a model download failure; `logs --init` shows the error |
| `examples/broken/bad-image.yaml` | `status` diagnoses an image pull failure |
| `examples/broken/oom.yaml` | `status` diagnoses out of memory |
| `examples/broken/unschedulable.yaml` | `status` diagnoses a scheduling failure |
| `examples/broken/no-runtime.yaml` | `status` diagnoses that no serving runtime supports the model format |
| `examples/sklearn-canary.yaml` | `status` shows stable 80%, canary `v2` 20% with its own replicas (needs Gateway API, see below) |
| HTTPRoute pointing at a Gateway that does not exist | `status` diagnoses the Gateway API problem (unit test with an edited fixture) |
| `examples/qwen-llm.yaml` (graphics processing unit, time-sliced) | Healthy; `status` and `logs` work. Its OpenAI-compatible endpoints are not covered by `test` in 0.1 |

### Exposure setups

Both setups must pass before a release. Fixtures for both are in `internal/status/testdata/v0.21` (Ingress) and `internal/status/testdata/v0.21-gateway-api`.

- **Kubernetes Ingress**: KServe's default.
- **Gateway API**: the lab cluster has used it since 2026-10-29, with the existing Cilium Gateway `gateway/lab-gateway`. That Gateway accepts only `*.k8s.internal` host names, and other namespaces only on its HTTPS listener. The Helm values for `kserve-resources` are:
  ```
  kserve.controller.gateway.ingressGateway.enableGatewayApi=true
  kserve.controller.gateway.ingressGateway.kserveGateway=gateway/lab-gateway
  kserve.controller.gateway.ingressGateway.className=cilium
  kserve.controller.gateway.domain=k8s.internal
  kserve.controller.gateway.urlScheme=https
  ```
  Existing InferenceServices move to HTTPRoutes only on their next reconcile, and their old Ingress stays behind.

**Canary objects.** The canary predictor's Deployment, Service and pods carry the same `component=predictor` label as the stable predictor. Only the `app` label tells them apart: `isvc.<name>-predictor` for the stable one, `isvc.<name>-<canary>-predictor` for a canary. `status` counts their replicas separately, and `test` port-forwards to the stable Service. Sample: `examples/sklearn-canary.yaml`, verified with an HTTPRoute split of 80/20.

### Version matrix

Unit tests use objects captured from each supported line: v0.16 (oldest), v0.18 (middle) and v0.21 (newest). Continuous integration runs the unit tests on every pull request.

End-to-end tests run in continuous integration against a kind cluster with KServe installed, once per version in the matrix. They are not run on every pull request. This machine has no Docker or kind today, so those tests run only in continuous integration until a local setup exists.

## 8. Decisions

Decided 2026-10-29:

1. **KServe versions**: support v0.16 through v0.21 by detecting fields, not version numbers (section 2.1).
2. **Gateway API**: fully supported next to Kubernetes Ingress in `status` and `test` (sections 2.2, 4, 5), and tested on the lab cluster (section 7).
3. **`status` exit code**: informational by default; `--exit-code` turns "not Ready" into exit 1 (sections 3, 4).
4. **`test` output**: the user chooses with `-o pretty|summary|raw|json`; `pretty` is the default (section 5).
5. **Plugin name**: `isvc` (binary `kubectl-isvc`).
   - **Risk**: the krew naming guide says to avoid resource acronyms and abbreviations, and to prefix vendor identifiers. A reviewer may ask for a rename.
   - **Justification for the krew-index pull request**:
     - `isvc` is not a kubectl built-in abbreviation. It is the short name KServe itself registers on the InferenceService CustomResourceDefinition, so KServe users already type `kubectl get isvc`.
     - krew-index already accepts similar names: `cm`, `open-svc`, `pv-migrate`, `pvu`.
   - **Fallback** if a rename is requested: `kserve`. Not `kserve-isvc`, which is long (11 characters, against an average of about 8 in krew-index) and still contains the abbreviation.
   - **Cost of a later rename**: the repository name, the Go module path, the binary name, the krew manifest and the README. Keep the name in as few places in the code as possible, for example the root command's `Use` field and one constant, so a rename stays cheap.

### Still open

- Whether the newest KServe release at release time joins the matrix automatically, or only after it has been checked by hand.

## 9. Out of scope for version 0.1

| Item | Why it waits | Earliest |
|---|---|---|
| Knative (Serverless) mode | Different objects (Knative Services and Revisions) and different status fields; the biggest scope risk | 0.2 |
| `kubectl isvc list` | `kubectl get isvc` is good enough for 0.1 | 0.2 |
| ServingRuntime listing | Runtime information already appears in `status` | 0.2 |
| `--chat` for large language model runtimes (OpenAI-compatible `/openai/v1/chat/completions`) | A different request format and streaming responses | 0.2 |
| InferenceGraph (`ig`) | Its API is still `v1alpha1`, and it is rarely used | Later. First as a "used by graph X" line in `status`, then `kubectl isvc graph status` |
| TrainedModel (`tm`, multi-model serving) | Rarely used | Later |
| Creating or editing InferenceServices | YAML plus `kubectl apply` already does this well | Not planned |
| Saving non-JSON responses (for example generated images) to a file | Needs custom predictors with their own response formats | Later |

## 10. Implementation notes

- **Language and libraries**: Go, `spf13/cobra` for commands, `k8s.io/cli-runtime` for kubectl-compatible flags, and `k8s.io/client-go` for everything else: the dynamic client, the typed core and apps clients, and port-forward.
- **No KServe Go types.** Read InferenceService and ServingRuntime objects through the dynamic client as unstructured data. KServe's Go module pulls in a very large dependency tree and ties the plugin to one KServe version. This also makes it possible to support several KServe versions (section 2.1).
- **Version handling in one place.** A small internal package turns an unstructured InferenceService into one plugin-side model: mode, runtime, components, canaries and exposure. All the "field exists or not" fallbacks live there, so commands never branch on KServe versions themselves.
- **Gateway API objects** (HTTPRoute, Gateway) are read through the dynamic client too. If the Gateway API CustomResourceDefinitions are not installed in the cluster, that part is silently skipped.
- **Go module path**: `github.com/klahio/kubectl-isvc`
- **License**: Apache License 2.0 (`LICENSE`, included in every release archive and extracted by krew)
- **Release**:
  - GoReleaser and GitHub Actions build per-platform archives: Linux, macOS and Windows on amd64 and arm64, with sha256 checksums and fixed version tags.
  - A `.krew.yaml` template feeds the pull request to `krew-index`.
  - `krew-release-bot` handles later version bumps.
- **krew acceptance basics**: real value beyond plain kubectl, no overlap with an existing plugin, the `kubectl-` binary name, a valid plugin manifest, and per-platform archives.
