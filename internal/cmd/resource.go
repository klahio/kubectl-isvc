package cmd

import (
	"strings"
)

// Kind is a KServe resource kind the commands handle.
type Kind string

const (
	KindInferenceService    Kind = "InferenceService"
	KindLLMInferenceService Kind = "LLMInferenceService"
)

// Resource types written the way kubectl accepts them: short name, kind and plural. The API-group suffix
// ".serving.kserve.io" is accepted on every spelling.
var (
	supportedTypes = map[string]Kind{
		"isvc":                 KindInferenceService,
		"inferenceservice":     KindInferenceService,
		"inferenceservices":    KindInferenceService,
		"llmisvc":              KindLLMInferenceService,
		"llminferenceservice":  KindLLMInferenceService,
		"llminferenceservices": KindLLMInferenceService,
	}

	// Other KServe resources the plugin is meant to grow into (docs/mvp.md section 9). Naming them gives a clear
	// "not supported yet" instead of "unknown type".
	plannedTypes = map[string]string{
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

// resourceArg accepts NAME or TYPE/NAME, as kubectl does. A bare NAME means an InferenceService.
func resourceArg(arg string) (Kind, string, error) {
	kind, name, typed := strings.Cut(arg, "/")
	if !typed {
		name, kind = arg, "isvc"
	}
	if name == "" || strings.Contains(name, "/") {
		return "", "", usage("%q is not a resource name; use NAME or TYPE/NAME, for example isvc/NAME or llmisvc/NAME", arg)
	}
	k := strings.TrimSuffix(strings.ToLower(kind), ".serving.kserve.io")
	if found, ok := supportedTypes[k]; ok {
		return found, name, nil
	}
	if full, ok := plannedTypes[k]; ok {
		return "", "", usage("%s is not supported yet; this version handles InferenceServices (isvc/NAME) and LLMInferenceServices (llmisvc/NAME)", full)
	}
	return "", "", usage("unknown resource type %q; use NAME, isvc/NAME or llmisvc/NAME", kind)
}
