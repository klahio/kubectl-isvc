package llmisvc

import (
	"encoding/json"
	"os"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func load(t *testing.T) *unstructured.Unstructured {
	t.Helper()
	data, err := os.ReadFile("testdata/smollm.json")
	if err != nil {
		t.Fatal(err)
	}
	u := &unstructured.Unstructured{}
	if err := json.Unmarshal(data, &u.Object); err != nil {
		t.Fatal(err)
	}
	return u
}

// testdata/smollm.json is examples/llmisvc-smollm.yaml as the lab cluster returned it (KServe v0.21, v1alpha2).
func TestFromUnstructuredLabSample(t *testing.T) {
	s, err := FromUnstructured(load(t))
	if err != nil {
		t.Fatal(err)
	}
	if s.Ready() != "True" || s.APIVersion != "serving.kserve.io/v1alpha2" {
		t.Errorf("ready=%q apiVersion=%q", s.Ready(), s.APIVersion)
	}
	if s.ModelName != "HuggingFaceTB/SmolLM2-135M-Instruct" || s.ModelURI != "hf://HuggingFaceTB/SmolLM2-135M-Instruct" {
		t.Errorf("model = %q %q", s.ModelName, s.ModelURI)
	}
	if len(s.Addresses) != 1 || s.Addresses[0] != "http://smollm-kserve-workload-svc.kserve-test.svc.cluster.local" {
		t.Errorf("addresses = %v", s.Addresses)
	}
	if s.URL != "" || s.Router {
		t.Errorf("no router: url=%q router=%v", s.URL, s.Router)
	}
	if !s.WorkloadsReported || len(s.Workloads) != 1 || s.Workloads[0] != (WorkloadRef{Role: RolePrimary, Kind: "Deployment", Name: "smollm-kserve", ReadyReplicas: 1}) {
		t.Errorf("workloads = %+v", s.Workloads)
	}
	if len(s.Applied) != 1 || s.Applied[0] != "kserve-config-llm-template" {
		t.Errorf("applied = %v", s.Applied)
	}
	if s.Replicas != 1 {
		t.Errorf("replicas = %d", s.Replicas)
	}
}

// v1alpha1 (the only version in KServe v0.16) has no status.workloads.
func TestFromUnstructuredWithoutWorkloads(t *testing.T) {
	u := load(t)
	u.SetAPIVersion("serving.kserve.io/v1alpha1")
	unstructured.RemoveNestedField(u.Object, "status", "workloads")
	unstructured.RemoveNestedField(u.Object, "status", "appliedConfigs")
	s, err := FromUnstructured(u)
	if err != nil {
		t.Fatal(err)
	}
	if s.WorkloadsReported || len(s.Workloads) != 0 {
		t.Errorf("workloads must be unreported: %+v", s.Workloads)
	}
}

func TestRoleOf(t *testing.T) {
	tests := []struct {
		labels map[string]string
		want   string
	}{
		{map[string]string{LabelComponent: "workload", LabelRole: "both"}, RolePrimary},
		{map[string]string{LabelComponent: "workload", LabelRole: "decode"}, RolePrimary},
		{map[string]string{LabelComponent: "workload", LabelRole: "prefill"}, RolePrefill},
		{map[string]string{LabelComponent: "router-scheduler"}, RoleScheduler},
	}
	for _, tt := range tests {
		if got := RoleOf(tt.labels); got != tt.want {
			t.Errorf("RoleOf(%v) = %q, want %q", tt.labels, got, tt.want)
		}
	}
	if IsWorker(map[string]string{LabelLWSWorkerIndex: "0"}) || !IsWorker(map[string]string{LabelLWSWorkerIndex: "2"}) {
		t.Error("worker index 0 is the leader, others are workers")
	}
}
