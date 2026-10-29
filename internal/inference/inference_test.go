package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/klahio/kubectl-isvc/internal/isvc"
	"github.com/klahio/kubectl-isvc/internal/kube"
	"github.com/klahio/kubectl-isvc/internal/status"
)

const (
	v1Body = `{"instances":[[6.8,2.8,4.8,1.4]]}`
	v2Body = `{"inputs":[{"name":"input-0","shape":[1,4],"datatype":"FP32","data":[[6.8,2.8,4.8,1.4]]}]}`
)

func TestChooseProtocol(t *testing.T) {
	sklearn := &status.Runtime{Name: "kserve-sklearnserver", Found: true, ProtocolVersions: []string{"v1", "v2"}}
	triton := &status.Runtime{Name: "kserve-tritonserver", Found: true, ProtocolVersions: []string{"v2", "grpc-v2"}}
	tfServing := &status.Runtime{Name: "kserve-tensorflow-serving", Found: true, ProtocolVersions: []string{"grpc-v1", "v1"}}

	tests := []struct {
		name     string
		flag     string
		spec     string
		runtime  *status.Runtime
		body     string
		want     Protocol
		because  string
		errorHas string
	}{
		{name: "flag wins", flag: "v2", runtime: sklearn, body: `{}`, want: V2, because: "--protocol"},
		{name: "spec protocolVersion", spec: "v2", runtime: sklearn, body: `{}`, want: V2, because: "spec.predictor.model.protocolVersion"},
		{name: "input shape v1", runtime: sklearn, body: v1Body, want: V1, because: `input has "instances"`},
		{name: "input shape v2", runtime: sklearn, body: v2Body, want: V2, because: `input has "inputs"`},
		{name: "runtime's first protocol", runtime: triton, body: `{}`, want: V2, because: "first protocol of runtime kserve-tritonserver"},
		{name: "gRPC entries are skipped", runtime: tfServing, body: `{}`, want: V1, because: "first protocol of runtime kserve-tensorflow-serving"},
		{name: "default", body: `{}`, want: V1, because: "default"},
		{name: "flag contradicts input", flag: "v2", runtime: sklearn, body: v1Body, errorHas: "does not match the input"},
		{name: "runtime does not support it", runtime: triton, body: v1Body, errorHas: "supports v2, grpc-v2, not v1"},
		{name: "gRPC flag", flag: "grpc-v2", body: `{}`, errorHas: "HTTP requests only"},
		{name: "not an object", body: `[1,2]`, errorHas: "not a JSON object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &isvc.InferenceService{Model: isvc.Model{ProtocolVersion: tt.spec}}
			got, err := ChooseProtocol(tt.flag, m, tt.runtime, []byte(tt.body))
			if tt.errorHas != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errorHas) {
					t.Fatalf("err = %v, want it to mention %q", err, tt.errorHas)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Protocol != tt.want || got.Because != tt.because {
				t.Errorf("got %+v, want %s because %q", got, tt.want, tt.because)
			}
		})
	}
}

// server is a fake model server: it publishes /openapi.json with the given paths and answers inference requests.
func server(t *testing.T, paths ...string) (*httptest.Server, *http.Request) {
	t.Helper()
	var last http.Request
	doc := map[string]any{"paths": map[string]any{}}
	for _, p := range paths {
		doc["paths"].(map[string]any)[p] = map[string]any{}
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/openapi.json" {
			_ = json.NewEncoder(w).Encode(doc)
			return
		}
		last = *r.Clone(context.Background())
		body, _ := io.ReadAll(r.Body)
		last.Body = io.NopCloser(bytes.NewReader(body))
		if r.URL.Path != "/v1/models/sklearn-iris:predict" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"Model with name nope does not exist."}`)
			return
		}
		_, _ = io.WriteString(w, `{"predictions":[1]}`)
	}))
	t.Cleanup(s.Close)
	return s, &last
}

func TestCheckPath(t *testing.T) {
	s, _ := server(t, V1.PathTemplate())
	ep := &Endpoint{BaseURL: s.URL}
	if err := CheckPath(context.Background(), ep, V1); err != nil {
		t.Errorf("v1 is published: %v", err)
	}
	err := CheckPath(context.Background(), ep, V2)
	if err == nil || !strings.Contains(err.Error(), "use --protocol v1") {
		t.Errorf("err = %v, want a hint to use v1", err)
	}

	// No document at all: the check is skipped.
	plain := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(plain.Close)
	if err := CheckPath(context.Background(), &Endpoint{BaseURL: plain.URL}, V2); err != nil {
		t.Errorf("without /openapi.json the check must be skipped: %v", err)
	}
}

func TestSendAndOutputs(t *testing.T) {
	s, last := server(t, V1.PathTemplate())
	headers, err := ParseHeaders([]string{"Authorization: Bearer test-token"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Send(context.Background(), Request{
		Endpoint: &Endpoint{BaseURL: s.URL, Host: "sklearn-iris.kserve-test.example.com", Route: "Ingress sklearn-iris at 127.0.0.1 with Host x"},
		Protocol: Choice{V1, "input has \"instances\""},
		Model:    "sklearn-iris",
		Body:     []byte(v1Body),
		Headers:  headers,
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() || last.Host != "sklearn-iris.kserve-test.example.com" || last.Header.Get("Authorization") != "Bearer test-token" {
		t.Errorf("status=%d host=%q auth=%q", res.StatusCode, last.Host, last.Header.Get("Authorization"))
	}
	if ct := last.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}

	var out bytes.Buffer
	if err := Write(&out, OutputSummary, res); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.HasPrefix(got, "HTTP 200 · ") || !strings.HasSuffix(got, " · via Ingress sklearn-iris · predictions: [1]\n") {
		t.Errorf("summary = %q", got)
	}
	out.Reset()
	if err := Write(&out, OutputRaw, res); err != nil || out.String() != `{"predictions":[1]}` {
		t.Errorf("raw = %q, %v", out.String(), err)
	}
	out.Reset()
	if err := Write(&out, OutputJSON, res); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil || doc["statusCode"] != float64(200) || doc["protocol"] != "v1" {
		t.Errorf("json = %s, %v", out.String(), err)
	}
	out.Reset()
	if err := Write(&out, OutputPretty, res); err != nil || !strings.Contains(out.String(), "  Host: sklearn-iris.kserve-test.example.com\n") {
		t.Errorf("pretty = %q, %v", out.String(), err)
	}
}

func TestSummarizeV2AndErrors(t *testing.T) {
	v2 := &Result{JSONBody: map[string]any{"outputs": []any{map[string]any{"name": "output-0", "data": []any{1.0}}}}}
	if got := summarize(v2); got != "outputs: output-0=[1]" {
		t.Errorf("v2 = %q", got)
	}
	failure := &Result{JSONBody: map[string]any{"error": "Model with name nope does not exist."}}
	if got := summarize(failure); got != `error: "Model with name nope does not exist."` {
		t.Errorf("error = %q", got)
	}
}

// With KServe's default domain the external name does not resolve; the Ingress or Gateway address with the Host
// header still reaches the model without port-forward.
func TestDiscoverHostHeaderRoute(t *testing.T) {
	s, _ := server(t, V1.PathTemplate())
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(s.URL, "http://"))

	sum := &status.Summary{Endpoints: status.Endpoints{
		External: "http://sklearn-iris-kserve-test.example.invalid:" + port,
		Routable: false,
		Exposure: []status.Exposure{{Kind: "HTTPRoute", Name: "sklearn-iris", Via: "Gateway gateway/lab-gateway", Address: "127.0.0.1"}},
	}}
	snap := &status.Snapshot{ISVC: &isvc.InferenceService{Name: "sklearn-iris"}}
	ep, err := Discover(context.Background(), kube.Clients{}, sum, snap, DiscoverOptions{PortForward: PortForwardNever})
	if err != nil {
		t.Fatal(err)
	}
	if ep.BaseURL != "http://127.0.0.1:"+port || ep.Host != "sklearn-iris-kserve-test.example.invalid:"+port {
		t.Errorf("endpoint = %+v", ep)
	}
	if !strings.HasPrefix(ep.Route, "HTTPRoute sklearn-iris at 127.0.0.1") {
		t.Errorf("route = %q", ep.Route)
	}
}

func TestDiscoverURLFlag(t *testing.T) {
	ep, err := Discover(context.Background(), kube.Clients{}, &status.Summary{}, &status.Snapshot{}, DiscoverOptions{URL: "http://localhost:8080/"})
	if err != nil || ep.BaseURL != "http://localhost:8080" || ep.Route != "--url" {
		t.Errorf("endpoint = %+v, %v", ep, err)
	}
}

func TestTargetPrefersTransformer(t *testing.T) {
	m := &isvc.InferenceService{Components: []isvc.Component{{Name: "predictor"}, {Name: "transformer"}}}
	if Target(m) != "transformer" {
		t.Error("requests must go through the transformer")
	}
	if Target(&isvc.InferenceService{Components: []isvc.Component{{Name: "predictor"}}}) != "predictor" {
		t.Error("without a transformer the predictor receives the request")
	}
}

func TestContainerPort(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{
		Name: "kserve-container", Ports: []corev1.ContainerPort{{Name: "h2c", ContainerPort: 8080}},
	}}}}
	if p, err := containerPort(pod, intstr.FromInt32(8080)); err != nil || p != 8080 {
		t.Errorf("number: %d, %v", p, err)
	}
	if p, err := containerPort(pod, intstr.FromString("h2c")); err != nil || p != 8080 {
		t.Errorf("name: %d, %v", p, err)
	}
	if _, err := containerPort(pod, intstr.FromString("missing")); err == nil {
		t.Error("unknown port name must fail")
	}
}

func TestParseHeaders(t *testing.T) {
	if _, err := ParseHeaders([]string{"no-colon"}); err == nil {
		t.Error("a header without a colon must be rejected")
	}
	h, err := ParseHeaders([]string{"X-A: 1", "X-A: 2"})
	if err != nil || len(h.Values("X-A")) != 2 {
		t.Errorf("repeated headers: %v, %v", h, err)
	}
}
