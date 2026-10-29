package cmd

import (
	"strings"
)

// Resource types written the way kubectl accepts them: short name, kind, plural, and plural with the API group.
var (
	inferenceServiceTypes = []string{"isvc", "inferenceservice", "inferenceservices", "inferenceservices.serving.kserve.io"}

	// Other KServe resources the plugin is meant to grow into (docs/mvp.md section 9). Naming them gives a clear
	// "not supported yet" instead of "unknown type".
	plannedTypes = map[string]string{
		"llmisvc":               "LLMInferenceService",
		"llminferenceservice":   "LLMInferenceService",
		"llminferenceservices":  "LLMInferenceService",
		"ig":                    "InferenceGraph",
		"inferencegraph":        "InferenceGraph",
		"inferencegraphs":       "InferenceGraph",
		"tm":                    "TrainedModel",
		"trainedmodel":          "TrainedModel",
		"trainedmodels":         "TrainedModel",
		"servingruntime":        "ServingRuntime",
		"servingruntimes":       "ServingRuntime",
		"clusterservingruntime": "ClusterServingRuntime",
	}
)

// inferenceServiceName accepts NAME or TYPE/NAME, as kubectl does, and returns the InferenceService name.
// A bare NAME means an InferenceService.
func inferenceServiceName(arg string) (string, error) {
	kind, name, typed := strings.Cut(arg, "/")
	if !typed {
		name, kind = arg, "isvc"
	}
	if name == "" || strings.Contains(name, "/") {
		return "", usage("%q is not a resource name; use NAME or isvc/NAME", arg)
	}
	k := strings.ToLower(kind)
	for _, t := range inferenceServiceTypes {
		if k == t || k == strings.TrimSuffix(t, ".serving.kserve.io") {
			return name, nil
		}
	}
	if full, ok := plannedTypes[strings.TrimSuffix(k, ".serving.kserve.io")]; ok {
		return "", usage("%s is not supported yet; this version handles InferenceServices (isvc/NAME) only", full)
	}
	return "", usage("unknown resource type %q; use NAME or isvc/NAME", kind)
}
