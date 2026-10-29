package status

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Each deliberately broken sample from examples/broken must be diagnosed with the right cause (docs/mvp.md section 7).
func TestDiagnoseBrokenSamples(t *testing.T) {
	tests := []struct {
		name      string
		wantTitle string
		wantText  string
	}{
		{"bad-storage", "Model download failed", "storage-initializer exited with code 1"},
		{"bad-image", "Image cannot be pulled", "kserve/sklearnserver:v0.0.0-does-not-exist"},
		{"oom", "Out of memory", "memory limit (32Mi)"},
		{"unschedulable", "Pod cannot be scheduled", "Insufficient nvidia.com/gpu"},
		{"no-runtime", "No serving runtime supports this model", `"no-such-format"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := Diagnose(loadFixture(t, tt.name).snapshot(t))
			errs := errorsOnly(findings)
			if len(errs) != 1 {
				t.Fatalf("want exactly one error finding, got %d: %+v", len(errs), findings)
			}
			got := errs[0]
			if got.Title != tt.wantTitle {
				t.Errorf("title = %q, want %q", got.Title, tt.wantTitle)
			}
			if text := strings.Join(got.Details, "\n"); !strings.Contains(text, tt.wantText) {
				t.Errorf("details %q do not mention %q", text, tt.wantText)
			}
		})
	}
}

func TestDiagnoseHealthyButIngressNotRoutable(t *testing.T) {
	findings := Diagnose(loadFixture(t, "sklearn-iris").snapshot(t))
	if len(errorsOnly(findings)) != 0 {
		t.Fatalf("healthy sample should have no errors: %+v", findings)
	}
	if len(findings) != 1 {
		t.Fatalf("want one warning, got %+v", findings)
	}
	f := findings[0]
	if f.Title != "External address not routable" || f.Object != "ingress/sklearn-iris" {
		t.Errorf("unexpected finding %+v", f)
	}
	// The lab cluster's trap: KServe says the ingress is ready although its class does not exist.
	want := []string{`IngressClass "istio" does not exist`, "KServe still reports IngressReady=True"}
	for _, w := range want {
		if !strings.Contains(strings.Join(f.Details, "\n"), w) {
			t.Errorf("details %q do not mention %q", f.Details, w)
		}
	}
}

func TestDiagnoseStorageFailureLogsFromPreviousInstance(t *testing.T) {
	f := Diagnose(loadFixture(t, "bad-storage").snapshot(t))[0]
	if f.Logs == nil || f.Logs.Container != "storage-initializer" {
		t.Fatalf("want logs of storage-initializer, got %+v", f.Logs)
	}
}

func TestDiagnoseMergesIdenticalReplicas(t *testing.T) {
	s := loadFixture(t, "oom").snapshot(t)
	second := *s.Pods[0].DeepCopy()
	second.Name += "-2"
	s.Pods = append(s.Pods, second)

	errs := errorsOnly(Diagnose(s))
	if len(errs) != 1 {
		t.Fatalf("want one merged finding, got %+v", errs)
	}
	if last := errs[0].Details[len(errs[0].Details)-1]; last != "same problem in 2 pods" {
		t.Errorf("last detail = %q", last)
	}
}

func TestDiagnoseGatewayAPI(t *testing.T) {
	route := func(accepted string) unstructured.Unstructured {
		return unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "gateway.networking.k8s.io/v1",
			"kind":       "HTTPRoute",
			"metadata":   map[string]any{"name": "sklearn-iris", "namespace": "kserve-test"},
			"spec": map[string]any{"parentRefs": []any{
				map[string]any{"name": "lab-gateway", "namespace": "gateway"},
			}},
			"status": map[string]any{"parents": []any{map[string]any{
				"parentRef": map[string]any{"name": "lab-gateway"},
				"conditions": []any{
					map[string]any{"type": "Accepted", "status": accepted, "reason": "NotAllowedByListeners"},
					map[string]any{"type": "ResolvedRefs", "status": "True"},
				},
			}}},
		}}
	}

	tests := []struct {
		name     string
		accepted string
		gateways bool
		want     []string
	}{
		{"accepted and gateway programmed", "True", true, nil},
		{"route rejected", "False", true, []string{"Accepted is not True"}},
		{"gateway missing", "True", false, []string{"Gateway gateway/lab-gateway does not exist"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := loadFixture(t, "sklearn-iris")
			s := f.snapshot(t)
			s.Ingresses = nil
			s.HTTPRoutes = []unstructured.Unstructured{route(tt.accepted)}
			if !tt.gateways {
				s.Gateways = nil
			}
			var got []string
			for _, finding := range exposureFindings(s) {
				got = append(got, finding.Details...)
			}
			if len(tt.want) == 0 && len(got) != 0 {
				t.Fatalf("want no findings, got %q", got)
			}
			for _, w := range tt.want {
				if !strings.Contains(strings.Join(got, "\n"), w) {
					t.Errorf("findings %q do not mention %q", got, w)
				}
			}
		})
	}
}

func TestIsNoise(t *testing.T) {
	noise := corev1.Event{Type: "Warning", Reason: "UpdateFailed",
		Message: `Failed to update status for InferenceService "x": Operation cannot be fulfilled ...: the object has been modified; please apply your changes`}
	if !IsNoise(noise) {
		t.Error("optimistic-locking retry should be noise")
	}
	real := corev1.Event{Type: "Warning", Reason: "FailedScheduling", Message: "0/1 nodes are available"}
	if IsNoise(real) {
		t.Error("scheduling failure is not noise")
	}
}
