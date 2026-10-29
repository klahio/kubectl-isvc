# kubectl-isvc

A kubectl plugin for day-to-day operation of a single [KServe](https://kserve.github.io/website/) **InferenceService**.

One InferenceService turns into many Kubernetes objects: a serving runtime, Deployments, pods with a model-download init container, Services, an Ingress or Gateway API HTTPRoutes, and an autoscaler. When a model is not serving, the reason is usually several objects away. KServe's own status often doesn't help: when a pod fails, the reason it reports is "Predictor ingress not created". `kubectl isvc` answers three questions with one command each:

| Question | Command |
|---|---|
| Is my model serving, and if not, why? | `kubectl isvc status NAME` |
| Does it actually answer a request? | `kubectl isvc test NAME -f input.json` |
| What did its containers print? | `kubectl isvc logs NAME` |

`isvc` is the short name KServe registers for InferenceService, so the plugin reads like `kubectl get isvc`.

## Install

With [krew](https://krew.sigs.k8s.io/) (once the plugin is in the krew index):

```bash
kubectl krew install isvc
```

From a release: download the archive for your platform from [Releases](https://github.com/klahio/kubectl-isvc/releases), then put `kubectl-isvc` on your `PATH`.

From source (Go 1.26 or newer):

```bash
go install github.com/klahio/kubectl-isvc@latest
```

## Usage

### status: summary and diagnosis

```console
$ kubectl isvc status sklearn-canary -n kserve-test
sklearn-canary   namespace: kserve-test   mode: Standard   Ready   age: 14m

Endpoints
  external     https://sklearn-canary-kserve-test.k8s.internal
  in-cluster   http://sklearn-canary-predictor.kserve-test.svc.cluster.local
  httproute    sklearn-canary   via Gateway gateway/lab-gateway   address: 192.168.4.30

Model
  format    sklearn
  storage   gs://kfserving-examples/models/sklearn/1.0/model
  runtime   kserve-sklearnserver (ClusterServingRuntime)   protocols: v1, v2
  state     Loaded (UpToDate)

Components
  predictor   Ready   replicas 1/1   autoscaling 1–1 (cpu 80%)

Canary
  stable   80%
  v2       20%   Ready   replicas 1/1

Conditions
  Ready                  True
  CanaryPredictorReady   True   AllCanariesReady
  ...
```

When something is wrong, `status` follows the chain InferenceService → Deployment → pod → container and names the cause:

```console
$ kubectl isvc status bad-storage -n kserve-test
...
Diagnosis
  ✗ Model download failed   predictor pod/bad-storage-predictor-f4cfc7765-wxst8
      storage-initializer exited with code 1 (restarts: 15)
      ... Storage initialization failed: GCS resource 'kfserving-examples' not found.
      last log lines of storage-initializer:
        ...
      hint: check spec.predictor.model.storageUri and the storage credentials
```

What it diagnoses:
- model download failures
- images that cannot be pulled
- crashing servers and out-of-memory kills
- pods that cannot be scheduled (for example "Insufficient nvidia.com/gpu")
- model formats with no serving runtime
- pods rejected by a quota or an admission policy
- canaries that are not ready
- an external address that cannot be reached, for both Ingress and Gateway API, even when KServe reports `IngressReady=True`
- an Ingress left behind after switching KServe to Gateway API

| Flag | Meaning |
|---|---|
| `-o json\|yaml` | Structured summary for scripts |
| `--events` | Also list recent events (the controller's harmless update-conflict retries are filtered out) |
| `-w, --watch` | Redraw every 2 seconds |
| `--exit-code` | Exit 1 when the InferenceService is not Ready, for health checks |

### test: smoke-test inference request

```console
$ kubectl isvc test sklearn-iris -n kserve-test -f examples/iris-input.json
POST https://sklearn-iris-kserve-test.k8s.internal/v1/models/sklearn-iris:predict
  connected to 192.168.4.30:443
  protocol v1 (input has "instances") · via HTTPRoute sklearn-iris at 192.168.4.30:443
200 OK · 6 ms

{
  "predictions": [1, 1]
}
```

You don't need to know the address. `test` uses the first route that works:
1. `--url`, if given
2. the external address, when `status` considers it routable
3. the external host name connected to the Ingress or Gateway address (like `curl --resolve`), when the name does not resolve on your machine
4. a port-forward to a ready pod behind the stable predictor's (or transformer's) Service

You don't need to know the protocol either. `test` uses the first rule that gives an answer: `--protocol`, the InferenceService's `protocolVersion`, the shape of the input (`instances` means v1, `inputs` means the v2 Open Inference Protocol), the runtime's first protocol, and finally v1. If the choice contradicts the input or the runtime, or the server's `/openapi.json` lacks the path, `test` stops before sending anything.

| Flag | Meaning |
|---|---|
| `-f, --filename` | Request body; `-` reads standard input |
| `-o pretty\|summary\|raw\|json` | `summary` prints one line, `HTTP 200 · 1 ms · via port-forward · predictions: [1, 1]`; `raw` prints only the body, for `jq` |
| `--protocol v1\|v2`, `--model`, `--url` | Override the automatic choices |
| `--port-forward auto\|always\|never` | Route control |
| `-H "Name: value"` | Extra header, repeatable |

### logs: the right containers, without pod names

```bash
kubectl isvc logs sklearn-iris                # model server of every predictor pod
kubectl isvc logs sklearn-iris --init         # model download (storage-initializer)
kubectl isvc logs sklearn-iris -c all -f      # every component; follows new and restarted pods
kubectl isvc logs sklearn-iris -p             # the previous, crashed instance
```

When more than one container is shown, each line is prefixed with `[component/pod/container]`. `--since`, `--tail` (default 100 lines; `-1` for all) and `--prefix auto|true|false` work as you would expect.

### Common flags and exit codes

Every command takes the usual kubectl connection flags: `-n`, `--context`, `--kubeconfig`, `--as` and the others. `--no-color` or `NO_COLOR` turns colors off.

| Exit code | Meaning |
|---|---|
| 0 | Success |
| 1 | The command ran but the result is a failure: a non-2xx response, `status --exit-code` on a service that is not Ready, or no readable log |
| 2 | Wrong usage or bad input |
| 3 | Cluster unreachable, InferenceService not found, or permission denied |

## Compatibility

| | Supported |
|---|---|
| KServe | v0.16 – v0.21. Tested on v0.21.0; v0.16 – v0.20 are covered by comparing the KServe source of each release, not yet by tests |
| Deployment mode | Standard (formerly RawDeployment). Knative (Serverless) mode is not supported yet |
| Exposure | Kubernetes Ingress and Gateway API HTTPRoute |
| Canary rollouts | `spec.canary` (KServe v0.21, Standard mode) |

The plugin reads KServe objects as unstructured data and checks for fields rather than version numbers, so it does not depend on KServe's Go module.

## Permissions

`kubectl isvc` never creates, changes or deletes cluster objects. It needs:
- `get` and `list` on `inferenceservices`, `servingruntimes`, `clusterservingruntimes`, `pods`, `pods/log`, `deployments`, `horizontalpodautoscalers`, `services`, `ingresses`, `ingressclasses`, `httproutes`, `gateways` and `events`
- `create` on `pods/portforward`, for `test`

A missing permission on an optional object shows up as "not checked" instead of failing the command.

## Development

```bash
go test ./...                              # unit tests, using objects captured from a real cluster
go test ./internal/status -update          # refresh the golden output files after an intended layout change
hack/capture-fixtures.sh NAMESPACE DIR     # capture new fixtures from a cluster
go run github.com/goreleaser/goreleaser/v2@v2.18.3 release --snapshot --clean   # local release build
```

`examples/` holds the sample InferenceServices used for testing, including deliberately broken ones in `examples/broken/`. The specification and the design decisions are in [docs/mvp.md](docs/mvp.md).

## License

[Apache License 2.0](LICENSE)
