// Package isvc turns an unstructured KServe InferenceService into one plugin-side model.
//
// All "does this field exist in this KServe version" fallbacks live here (docs/mvp.md section 2.1),
// so the commands never branch on KServe versions themselves.
package isvc

import (
	"fmt"
	"sort"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Group/version/resources the plugin reads through the dynamic client.
var (
	InferenceServiceGVR      = schema.GroupVersionResource{Group: "serving.kserve.io", Version: "v1beta1", Resource: "inferenceservices"}
	ServingRuntimeGVR        = schema.GroupVersionResource{Group: "serving.kserve.io", Version: "v1alpha1", Resource: "servingruntimes"}
	ClusterServingRuntimeGVR = schema.GroupVersionResource{Group: "serving.kserve.io", Version: "v1alpha1", Resource: "clusterservingruntimes"}
)

// Labels and annotations KServe puts on the objects it creates.
const (
	LabelInferenceService    = "serving.kserve.io/inferenceservice"
	LabelComponent           = "component"
	AnnotationDeploymentMode = "serving.kserve.io/deploymentMode"
)

// Deployment modes. RawDeployment is the pre-v0.16 name of Standard; objects created before an upgrade may still carry it.
const (
	ModeStandard      = "Standard"
	ModeRawDeployment = "RawDeployment"
)

// Container names KServe uses in predictor, transformer and explainer pods.
const (
	ContainerServer         = "kserve-container"
	ContainerStorageInitial = "storage-initializer"
)

// Condition is one entry of status.conditions.
type Condition struct {
	Type               string    `json:"type"`
	Status             string    `json:"status"`
	Reason             string    `json:"reason,omitempty"`
	Message            string    `json:"message,omitempty"`
	Severity           string    `json:"severity,omitempty"`
	LastTransitionTime time.Time `json:"lastTransitionTime,omitempty"`
}

// IsTrue reports whether the condition's status is "True".
func (c Condition) IsTrue() bool { return c.Status == "True" }

// Model describes what is being served and by which runtime.
type Model struct {
	Format          string `json:"format,omitempty"`
	StorageURI      string `json:"storageUri,omitempty"`
	ProtocolVersion string `json:"protocolVersion,omitempty"`
	// Runtime is the selected ServingRuntime or ClusterServingRuntime name; empty when KServe did not record one.
	Runtime string `json:"runtime,omitempty"`
	// RuntimeKind is "ClusterServingRuntime" or "ServingRuntime".
	RuntimeKind string `json:"runtimeKind,omitempty"`
	// CustomContainer is true when the predictor brings its own container instead of a model plus runtime.
	CustomContainer bool `json:"customContainer,omitempty"`
}

// ModelStatus mirrors status.modelStatus.
type ModelStatus struct {
	ActiveState      string   `json:"activeState,omitempty"`
	TargetState      string   `json:"targetState,omitempty"`
	TransitionStatus string   `json:"transitionStatus,omitempty"`
	LastFailure      *Failure `json:"lastFailure,omitempty"`
}

// Failure mirrors status.modelStatus.lastFailureInfo.
type Failure struct {
	Reason   string `json:"reason,omitempty"`
	Message  string `json:"message,omitempty"`
	Location string `json:"location,omitempty"`
	ExitCode int64  `json:"exitCode,omitempty"`
}

// Component is a predictor, transformer or explainer that exists in the spec.
type Component struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

// Canary is one entry of spec.canary (KServe v0.21 and newer, Standard mode only).
type Canary struct {
	Name           string `json:"name"`
	TrafficPercent int64  `json:"trafficPercent"`
	// Ready comes from status.canaryStatuses: "True", "False" or "Unknown" when KServe has not reported it yet.
	Ready string `json:"ready"`
}

// LabelApp is the label that tells a component's objects apart from those of its canaries: KServe sets it to
// "isvc.<name>-<component>" for the stable component and "isvc.<name>-<canary>-<component>" for a canary.
const LabelApp = "app"

// AppLabel returns the "app" label value of a component, or of one of its canaries when canary is not empty.
func AppLabel(name, component, canary string) string {
	if canary == "" {
		return "isvc." + name + "-" + component
	}
	return "isvc." + name + "-" + canary + "-" + component
}

// CanaryOf returns the canary that an object with these labels belongs to, or "" for the stable component.
// Objects without the app label are treated as stable.
func (s *InferenceService) CanaryOf(labels map[string]string) string {
	app, component := labels[LabelApp], labels[LabelComponent]
	for _, c := range s.Canaries {
		if app == AppLabel(s.Name, component, c.Name) {
			return c.Name
		}
	}
	return ""
}

// InferenceService is the plugin's view of one InferenceService.
type InferenceService struct {
	Name       string            `json:"name"`
	Namespace  string            `json:"namespace"`
	UID        string            `json:"-"`
	Created    time.Time         `json:"created"`
	Mode       string            `json:"mode"`
	URL        string            `json:"url,omitempty"`
	AddressURL string            `json:"addressUrl,omitempty"`
	Conditions []Condition       `json:"conditions,omitempty"`
	Model      Model             `json:"model"`
	ModelState ModelStatus       `json:"modelStatus"`
	Components []Component       `json:"components"`
	Canaries   []Canary          `json:"canaries,omitempty"`
	Labels     map[string]string `json:"-"`
	// OlderThanSupported is set when fields that every supported KServe version writes are missing.
	OlderThanSupported bool `json:"olderThanSupported,omitempty"`
}

// Condition returns the condition of the given type, if present.
func (s *InferenceService) Condition(t string) (Condition, bool) {
	for _, c := range s.Conditions {
		if c.Type == t {
			return c, true
		}
	}
	return Condition{}, false
}

// Ready reports the Ready condition's status: "True", "False" or "Unknown" (also when the condition is missing).
func (s *InferenceService) Ready() string {
	if c, ok := s.Condition("Ready"); ok && c.Status != "" {
		return c.Status
	}
	return "Unknown"
}

// ModeSupported reports whether version 0.1 handles this deployment mode.
func (s *InferenceService) ModeSupported() bool {
	return s.Mode == ModeStandard || s.Mode == ModeRawDeployment
}

// FromUnstructured converts an InferenceService read through the dynamic client.
func FromUnstructured(u *unstructured.Unstructured) (*InferenceService, error) {
	if u.GetKind() != "InferenceService" {
		return nil, fmt.Errorf("expected kind InferenceService, got %q", u.GetKind())
	}
	obj := u.Object
	s := &InferenceService{
		Name:      u.GetName(),
		Namespace: u.GetNamespace(),
		UID:       string(u.GetUID()),
		Created:   u.GetCreationTimestamp().Time,
		Labels:    u.GetLabels(),
	}

	// status.deploymentMode exists from v0.16. The annotation is set by KServe's webhook on every version and
	// also covers a brand-new object whose status is still empty.
	statusMode := str(obj, "status", "deploymentMode")
	s.Mode = statusMode
	if s.Mode == "" {
		s.Mode = u.GetAnnotations()[AnnotationDeploymentMode]
	}

	s.URL = str(obj, "status", "url")
	s.AddressURL = str(obj, "status", "address", "url")
	s.Conditions = conditions(obj)

	s.Model = model(obj)
	s.ModelState = modelStatus(obj)

	for _, name := range []string{"predictor", "transformer", "explainer"} {
		if _, found, _ := unstructured.NestedMap(obj, "spec", name); !found {
			continue
		}
		s.Components = append(s.Components, Component{Name: name, URL: str(obj, "status", "components", name, "url")})
	}

	canaries, _, _ := unstructured.NestedSlice(obj, "spec", "canary")
	for _, c := range canaries {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		s.Canaries = append(s.Canaries, Canary{Name: str(m, "predictor", "name"), TrafficPercent: integer(m, "trafficPercent"), Ready: "Unknown"})
	}
	statuses, _, _ := unstructured.NestedSlice(obj, "status", "canaryStatuses")
	for _, st := range statuses {
		m, ok := st.(map[string]any)
		if !ok {
			continue
		}
		ready, found, _ := unstructured.NestedBool(m, "ready")
		for i := range s.Canaries {
			if s.Canaries[i].Name == str(m, "name") && found {
				s.Canaries[i].Ready = map[bool]string{true: "True", false: "False"}[ready]
			}
		}
	}

	// Every supported version (v0.16+) records status.deploymentMode once the controller has seen the object.
	s.OlderThanSupported = statusMode == "" && len(s.Conditions) > 0

	return s, nil
}

func model(obj map[string]any) Model {
	m := Model{
		Format:          str(obj, "spec", "predictor", "model", "modelFormat", "name"),
		StorageURI:      str(obj, "spec", "predictor", "model", "storageUri"),
		ProtocolVersion: str(obj, "spec", "predictor", "model", "protocolVersion"),
	}
	if m.StorageURI == "" {
		m.StorageURI = str(obj, "spec", "predictor", "model", "storage", "path")
	}
	if name := str(obj, "status", "clusterServingRuntimeName"); name != "" {
		m.Runtime, m.RuntimeKind = name, "ClusterServingRuntime"
	} else if name := str(obj, "status", "servingRuntimeName"); name != "" {
		m.Runtime, m.RuntimeKind = name, "ServingRuntime"
	}
	if _, found, _ := unstructured.NestedMap(obj, "spec", "predictor", "model"); !found {
		containers, _, _ := unstructured.NestedSlice(obj, "spec", "predictor", "containers")
		m.CustomContainer = len(containers) > 0
	}
	return m
}

func modelStatus(obj map[string]any) ModelStatus {
	ms := ModelStatus{
		ActiveState:      str(obj, "status", "modelStatus", "states", "activeModelState"),
		TargetState:      str(obj, "status", "modelStatus", "states", "targetModelState"),
		TransitionStatus: str(obj, "status", "modelStatus", "transitionStatus"),
	}
	if info, found, _ := unstructured.NestedMap(obj, "status", "modelStatus", "lastFailureInfo"); found {
		exit := integer(info, "exitCode")
		ms.LastFailure = &Failure{
			Reason:   str(info, "reason"),
			Message:  str(info, "message"),
			Location: str(info, "location"),
			ExitCode: exit,
		}
	}
	return ms
}

func conditions(obj map[string]any) []Condition {
	raw, _, _ := unstructured.NestedSlice(obj, "status", "conditions")
	out := make([]Condition, 0, len(raw))
	for _, r := range raw {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		c := Condition{
			Type:     str(m, "type"),
			Status:   str(m, "status"),
			Reason:   str(m, "reason"),
			Message:  str(m, "message"),
			Severity: str(m, "severity"),
		}
		if t, err := time.Parse(time.RFC3339, str(m, "lastTransitionTime")); err == nil {
			c.LastTransitionTime = t
		}
		out = append(out, c)
	}
	// Ready first, then the rest alphabetically, so the output order does not depend on the controller.
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Type == "Ready") != (out[j].Type == "Ready") {
			return out[i].Type == "Ready"
		}
		return out[i].Type < out[j].Type
	})
	return out
}

// integer reads a nested number. Objects from the API server hold int64, but plain encoding/json decoding (for example
// of saved objects) produces float64, which unstructured.NestedInt64 rejects.
func integer(obj map[string]any, fields ...string) int64 {
	v, found, _ := unstructured.NestedFieldNoCopy(obj, fields...)
	if !found {
		return 0
	}
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	default:
		return 0
	}
}

// str reads a nested string field and returns "" when it is missing or not a string.
func str(obj map[string]any, fields ...string) string {
	v, _, _ := unstructured.NestedString(obj, fields...)
	return v
}
