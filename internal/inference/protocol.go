// Package inference sends a smoke-test inference request to an InferenceService (docs/mvp.md section 5).
package inference

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/klahio/kubectl-kserve/internal/isvc"
	"github.com/klahio/kubectl-kserve/internal/status"
)

// Protocol is an HTTP inference protocol.
type Protocol string

const (
	V1 Protocol = "v1"
	V2 Protocol = "v2" // Open Inference Protocol
)

// PathTemplate is the request path as it appears in a model server's /openapi.json.
func (p Protocol) PathTemplate() string {
	if p == V2 {
		return "/v2/models/{model_name}/infer"
	}
	return "/v1/models/{model_name}:predict"
}

// Path is the request path for a model.
func (p Protocol) Path(model string) string {
	return strings.Replace(p.PathTemplate(), "{model_name}", model, 1)
}

// Choice is the selected protocol and the rule that selected it, so the output can explain itself.
type Choice struct {
	Protocol Protocol
	Because  string
}

// ShapeOf reports the protocol a request body is written for: a top-level "instances" key means v1, "inputs" means v2.
func ShapeOf(body []byte) (Protocol, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return "", fmt.Errorf("the input is not a JSON object: %w", err)
	}
	_, v1 := top["instances"]
	_, v2 := top["inputs"]
	switch {
	case v1 && !v2:
		return V1, nil
	case v2 && !v1:
		return V2, nil
	default:
		return "", nil
	}
}

// ChooseProtocol applies the rules of docs/mvp.md section 5, first match wins:
// --protocol, spec.predictor.model.protocolVersion, the input's shape, the runtime's first protocol, v1.
// It fails before any request is sent when the choice contradicts the input or the runtime.
func ChooseProtocol(flag string, m *isvc.InferenceService, rt *status.Runtime, body []byte) (Choice, error) {
	shape, err := ShapeOf(body)
	if err != nil {
		return Choice{}, err
	}

	var c Choice
	switch {
	case flag != "":
		p, err := parse(flag)
		if err != nil {
			return Choice{}, err
		}
		c = Choice{p, "--protocol"}
	case m.Model.ProtocolVersion != "":
		p, err := parse(m.Model.ProtocolVersion)
		if err != nil {
			return Choice{}, fmt.Errorf("spec.predictor.model.protocolVersion %q: %w", m.Model.ProtocolVersion, err)
		}
		c = Choice{p, "spec.predictor.model.protocolVersion"}
	case shape != "":
		c = Choice{shape, fmt.Sprintf("input has %q", topKey(shape))}
	case rt != nil && firstHTTP(rt.ProtocolVersions) != "":
		c = Choice{firstHTTP(rt.ProtocolVersions), "first protocol of runtime " + rt.Name}
	default:
		c = Choice{V1, "default"}
	}

	if shape != "" && shape != c.Protocol {
		return Choice{}, fmt.Errorf("protocol %s (from %s) does not match the input, which has %q (a %s request); use --protocol %s or change the input",
			c.Protocol, c.Because, topKey(shape), shape, shape)
	}
	if rt != nil && rt.Found && len(rt.ProtocolVersions) > 0 && !slices.Contains(rt.ProtocolVersions, string(c.Protocol)) {
		return Choice{}, fmt.Errorf("runtime %s supports %s, not %s", rt.Name, strings.Join(rt.ProtocolVersions, ", "), c.Protocol)
	}
	return c, nil
}

func parse(s string) (Protocol, error) {
	switch strings.ToLower(s) {
	case "v1":
		return V1, nil
	case "v2":
		return V2, nil
	case "grpc-v1", "grpc-v2":
		return "", fmt.Errorf("%s is a gRPC protocol; version 0.1 sends HTTP requests only (v1 or v2)", s)
	default:
		return "", fmt.Errorf("unknown protocol %q (use v1 or v2)", s)
	}
}

// firstHTTP returns the first HTTP protocol a runtime lists, skipping gRPC ones.
func firstHTTP(versions []string) Protocol {
	for _, v := range versions {
		if p, err := parse(v); err == nil {
			return p
		}
	}
	return ""
}

func topKey(p Protocol) string {
	if p == V2 {
		return "inputs"
	}
	return "instances"
}
