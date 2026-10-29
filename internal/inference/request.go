package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// openAPITimeout bounds the /openapi.json look-up, which is only a safety check.
const openAPITimeout = 3 * time.Second

// Request is one smoke-test request.
type Request struct {
	Endpoint *Endpoint
	Protocol Choice
	Model    string
	Body     []byte
	Headers  http.Header
	Timeout  time.Duration
}

// Result is what came back.
type Result struct {
	Route      string        `json:"route"`
	Protocol   Protocol      `json:"protocol"`
	ProtocolBy string        `json:"protocolChosenBy"`
	Method     string        `json:"method"`
	URL        string        `json:"url"`
	ConnectTo  string        `json:"connectTo,omitempty"`
	StatusCode int           `json:"statusCode"`
	Status     string        `json:"status"`
	Duration   time.Duration `json:"-"`
	DurationMs int64         `json:"durationMs"`
	Body       []byte        `json:"-"`
	// JSONBody is the parsed body for -o json; it is nil when the body is not JSON.
	JSONBody any `json:"body,omitempty"`
	// TextBody holds a body that is not JSON.
	TextBody string `json:"bodyText,omitempty"`
}

// OK reports a 2xx response.
func (r *Result) OK() bool { return r.StatusCode >= 200 && r.StatusCode < 300 }

// CheckPath looks the request path up in the server's /openapi.json, if it publishes one, so a request that is
// bound to fail is never sent. A server without the document (or an unreadable one) skips the check.
func CheckPath(ctx context.Context, ep *Endpoint, p Protocol) error {
	ctx, cancel := context.WithTimeout(ctx, openAPITimeout)
	defer cancel()
	req, err := newRequest(ctx, http.MethodGet, ep, "/openapi.json", nil)
	if err != nil {
		return nil
	}
	resp, err := ep.client().Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var doc struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&doc); err != nil || len(doc.Paths) == 0 {
		return nil
	}
	if _, ok := doc.Paths[p.PathTemplate()]; ok {
		return nil
	}
	var known []string
	for _, candidate := range []Protocol{V1, V2} {
		if _, ok := doc.Paths[candidate.PathTemplate()]; ok {
			known = append(known, string(candidate))
		}
	}
	hint := "the server publishes neither the v1 nor the v2 inference path; it may use its own API (see its /openapi.json)"
	if len(known) > 0 {
		hint = "the server supports " + strings.Join(known, ", ") + "; use --protocol " + known[0]
	}
	return fmt.Errorf("the server's /openapi.json has no %s path (%s): %s", p, p.PathTemplate(), hint)
}

// Send performs the request.
func Send(ctx context.Context, r Request) (*Result, error) {
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	path := r.Protocol.Protocol.Path(r.Model)
	req, err := newRequest(ctx, http.MethodPost, r.Endpoint, path, r.Body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, vs := range r.Headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	start := time.Now()
	resp, err := r.Endpoint.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("request to %s failed: %w", req.URL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	elapsed := time.Since(start)
	if err != nil {
		return nil, fmt.Errorf("reading the response: %w", err)
	}

	res := &Result{
		Route:      r.Endpoint.Route,
		Protocol:   r.Protocol.Protocol,
		ProtocolBy: r.Protocol.Because,
		Method:     http.MethodPost,
		URL:        req.URL.String(),
		ConnectTo:  r.Endpoint.ConnectTo,
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Duration:   elapsed,
		DurationMs: elapsed.Milliseconds(),
		Body:       body,
	}
	var parsed any
	if json.Unmarshal(body, &parsed) == nil {
		res.JSONBody = parsed
	} else {
		res.TextBody = string(body)
	}
	return res, nil
}

func newRequest(ctx context.Context, method string, ep *Endpoint, path string, body []byte) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	return http.NewRequestWithContext(ctx, method, ep.BaseURL+path, reader)
}

// ParseHeaders turns repeated "Name: value" flags into a header set.
func ParseHeaders(values []string) (http.Header, error) {
	h := http.Header{}
	for _, v := range values {
		name, value, ok := strings.Cut(v, ":")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("header %q must look like \"Name: value\"", v)
		}
		h.Add(strings.TrimSpace(name), strings.TrimSpace(value))
	}
	return h, nil
}
