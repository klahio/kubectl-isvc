package isvc

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func object(status map[string]any, annotations map[string]any, spec map[string]any) *unstructured.Unstructured {
	if spec == nil {
		spec = map[string]any{"predictor": map[string]any{"model": map[string]any{
			"modelFormat": map[string]any{"name": "sklearn"},
			"storageUri":  "gs://bucket/model",
		}}}
	}
	obj := map[string]any{
		"apiVersion": "serving.kserve.io/v1beta1",
		"kind":       "InferenceService",
		"metadata":   map[string]any{"name": "m", "namespace": "ns", "annotations": annotations},
		"spec":       spec,
	}
	if status != nil {
		obj["status"] = status
	}
	return &unstructured.Unstructured{Object: obj}
}

func TestModeDetection(t *testing.T) {
	tests := []struct {
		name          string
		status        map[string]any
		annotations   map[string]any
		wantMode      string
		wantSupported bool
		wantOlder     bool
	}{
		{
			name:          "v0.16+ records the mode in the status",
			status:        map[string]any{"deploymentMode": "Standard", "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}},
			annotations:   map[string]any{AnnotationDeploymentMode: "Standard"},
			wantMode:      "Standard",
			wantSupported: true,
		},
		{
			name:          "brand-new object without status falls back to the annotation",
			annotations:   map[string]any{AnnotationDeploymentMode: "Standard"},
			wantMode:      "Standard",
			wantSupported: true,
		},
		{
			name:          "objects from before the rename keep RawDeployment",
			status:        map[string]any{"deploymentMode": "RawDeployment", "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}},
			wantMode:      "RawDeployment",
			wantSupported: true,
		},
		{
			name:      "Knative mode is not supported in version 0.1",
			status:    map[string]any{"deploymentMode": "Knative", "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}},
			wantMode:  "Knative",
			wantOlder: false,
		},
		{
			name:          "older than v0.16: conditions but no status.deploymentMode",
			status:        map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "True"}}},
			annotations:   map[string]any{AnnotationDeploymentMode: "RawDeployment"},
			wantMode:      "RawDeployment",
			wantSupported: true,
			wantOlder:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := FromUnstructured(object(tt.status, tt.annotations, nil))
			if err != nil {
				t.Fatal(err)
			}
			if s.Mode != tt.wantMode || s.ModeSupported() != tt.wantSupported || s.OlderThanSupported != tt.wantOlder {
				t.Errorf("mode=%q supported=%v older=%v, want %q %v %v",
					s.Mode, s.ModeSupported(), s.OlderThanSupported, tt.wantMode, tt.wantSupported, tt.wantOlder)
			}
		})
	}
}

func TestCanaryAndComponents(t *testing.T) {
	spec := map[string]any{
		"predictor":   map[string]any{"model": map[string]any{"modelFormat": map[string]any{"name": "sklearn"}}},
		"transformer": map[string]any{"containers": []any{}},
		"canary": []any{
			map[string]any{"trafficPercent": int64(20), "predictor": map[string]any{"name": "v2"}},
		},
	}
	s, err := FromUnstructured(object(map[string]any{"deploymentMode": "Standard"}, nil, spec))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Components) != 2 || s.Components[0].Name != "predictor" || s.Components[1].Name != "transformer" {
		t.Errorf("components = %+v", s.Components)
	}
	if len(s.Canaries) != 1 || s.Canaries[0].Name != "v2" || s.Canaries[0].TrafficPercent != 20 {
		t.Errorf("canaries = %+v", s.Canaries)
	}
}

func TestReadyIsUnknownWithoutConditions(t *testing.T) {
	s, err := FromUnstructured(object(nil, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if s.Ready() != "Unknown" {
		t.Errorf("Ready() = %q", s.Ready())
	}
}

func TestConditionsReadyFirst(t *testing.T) {
	status := map[string]any{"conditions": []any{
		map[string]any{"type": "PredictorReady", "status": "True"},
		map[string]any{"type": "IngressReady", "status": "True"},
		map[string]any{"type": "Ready", "status": "True"},
	}}
	s, err := FromUnstructured(object(status, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	got := []string{s.Conditions[0].Type, s.Conditions[1].Type, s.Conditions[2].Type}
	want := []string{"Ready", "IngressReady", "PredictorReady"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestCanaryOf(t *testing.T) {
	s := &InferenceService{Name: "m", Canaries: []Canary{{Name: "v2"}}}
	stable := map[string]string{LabelApp: "isvc.m-predictor", LabelComponent: "predictor"}
	canary := map[string]string{LabelApp: "isvc.m-v2-predictor", LabelComponent: "predictor"}
	if s.CanaryOf(stable) != "" || s.CanaryOf(canary) != "v2" || s.CanaryOf(map[string]string{}) != "" {
		t.Errorf("stable=%q canary=%q unlabeled=%q", s.CanaryOf(stable), s.CanaryOf(canary), s.CanaryOf(map[string]string{}))
	}
}
