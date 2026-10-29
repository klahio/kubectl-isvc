package cmd

import (
	"errors"
	"strings"
	"testing"
)

func TestInferenceServiceName(t *testing.T) {
	tests := []struct {
		arg      string
		want     string
		errorHas string
	}{
		{arg: "sklearn-iris", want: "sklearn-iris"},
		{arg: "isvc/sklearn-iris", want: "sklearn-iris"},
		{arg: "InferenceService/sklearn-iris", want: "sklearn-iris"},
		{arg: "inferenceservices.serving.kserve.io/sklearn-iris", want: "sklearn-iris"},
		{arg: "llmisvc/qwen", errorHas: "LLMInferenceService is not supported yet"},
		{arg: "ig/graph", errorHas: "InferenceGraph is not supported yet"},
		{arg: "deploy/x", errorHas: `unknown resource type "deploy"`},
		{arg: "isvc/", errorHas: "is not a resource name"},
		{arg: "isvc/a/b", errorHas: "is not a resource name"},
	}
	for _, tt := range tests {
		t.Run(tt.arg, func(t *testing.T) {
			got, err := inferenceServiceName(tt.arg)
			if tt.errorHas == "" {
				if err != nil || got != tt.want {
					t.Fatalf("got %q, %v; want %q", got, err, tt.want)
				}
				return
			}
			var exitErr *ExitError
			if !errors.As(err, &exitErr) || exitErr.Code != ExitUsage || !strings.Contains(err.Error(), tt.errorHas) {
				t.Fatalf("err = %v, want a usage error mentioning %q", err, tt.errorHas)
			}
		})
	}
}
