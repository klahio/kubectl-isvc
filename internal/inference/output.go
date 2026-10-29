package inference

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Output formats of `test -o` (docs/mvp.md section 5).
const (
	OutputPretty  = "pretty"
	OutputSummary = "summary"
	OutputRaw     = "raw"
	OutputJSON    = "json"
)

// Outputs lists the accepted formats.
var Outputs = []string{OutputPretty, OutputSummary, OutputRaw, OutputJSON}

// summaryBodyLimit keeps the one-line summary on one terminal line.
const summaryBodyLimit = 120

// Write prints the result in the chosen format.
func Write(w io.Writer, format string, r *Result) error {
	switch format {
	case OutputRaw:
		_, err := w.Write(r.Body)
		return err
	case OutputJSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	case OutputSummary:
		_, err := fmt.Fprintf(w, "HTTP %d · %d ms · via %s · %s\n", r.StatusCode, r.DurationMs, shortRoute(r.Route), summarize(r))
		return err
	default:
		var b strings.Builder
		fmt.Fprintf(&b, "%s %s\n", r.Method, r.URL)
		if r.Host != "" {
			fmt.Fprintf(&b, "  Host: %s\n", r.Host)
		}
		fmt.Fprintf(&b, "  protocol %s (%s) · via %s\n", r.Protocol, r.ProtocolBy, r.Route)
		fmt.Fprintf(&b, "%s · %d ms\n\n", r.Status, r.DurationMs)
		b.WriteString(prettyBody(r))
		_, err := io.WriteString(w, b.String())
		return err
	}
}

func prettyBody(r *Result) string {
	var buf bytes.Buffer
	if r.JSONBody != nil && json.Indent(&buf, r.Body, "", "  ") == nil {
		buf.WriteByte('\n')
		return buf.String()
	}
	s := string(r.Body)
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return s
}

// summarize picks the interesting part of a body: v1 predictions, v2 outputs, an error message, or the body itself.
func summarize(r *Result) string {
	obj, ok := r.JSONBody.(map[string]any)
	if !ok {
		return shorten(strings.TrimSpace(string(r.Body)))
	}
	if p, ok := obj["predictions"]; ok {
		return "predictions: " + shorten(compact(p))
	}
	if outs, ok := obj["outputs"].([]any); ok {
		var parts []string
		for _, o := range outs {
			om, _ := o.(map[string]any)
			parts = append(parts, fmt.Sprintf("%v=%s", om["name"], compact(om["data"])))
		}
		return "outputs: " + shorten(strings.Join(parts, " "))
	}
	for _, key := range []string{"error", "detail", "message"} {
		if v, ok := obj[key]; ok {
			return key + ": " + shorten(compact(v))
		}
	}
	return shorten(compact(obj))
}

func compact(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return strings.ReplaceAll(string(data), ",", ", ")
}

func shorten(s string) string {
	if len(s) <= summaryBodyLimit {
		return s
	}
	return s[:summaryBodyLimit] + "…"
}

// shortRoute keeps the first words of a route ("port-forward", "external address", "Ingress …").
func shortRoute(route string) string {
	if strings.HasPrefix(route, "port-forward") {
		return "port-forward"
	}
	if i := strings.Index(route, " at "); i > 0 {
		return route[:i]
	}
	return route
}
