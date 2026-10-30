package status

import (
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/duration"
)

// ScanLines is how many lines of a crashed container's log are searched for a root cause. vLLM prints its traceback
// twice and much start-up output after the cause: on the lab cluster the cause was 490 lines before the end.
const ScanLines = 2000

// LogCause is a known start-up failure that can be recognized in a log (docs/mvp.md section 6a.2).
type LogCause struct {
	// Any of these substrings (compared case-insensitively) identifies the cause.
	Patterns []string
	Meaning  string
	Hint     string
}

// knownCauses is the starting set from the specification; each new case gets a fixture.
var knownCauses = []LogCause{
	{
		Patterns: []string{"cannot find -l:libcuda.so.1"},
		Meaning:  "Triton cannot find the NVIDIA driver library libcuda.so.1",
		Hint:     "set TRITON_LIBCUDA_PATH to the node's driver directory: /usr/lib/x86_64-linux-gnu on Ubuntu or Debian nodes, /usr/lib64 on RHEL-family nodes",
	},
	{
		Patterns: []string{"cuda out of memory", "no available memory for the cache blocks", "torch.outofmemoryerror"},
		Meaning:  "GPU memory is too small for the model and its KV cache",
		Hint:     "lower --max-model-len, raise --gpu-memory-utilization, or use a larger GPU",
	},
	{
		Patterns: []string{"no cuda gpus are available", "failed to infer device type"},
		Meaning:  "no GPU is visible in the container",
		Hint:     "check the GPU request (nvidia.com/gpu) and, where the cluster needs one, runtimeClassName",
	},
	{
		Patterns: []string{"gated repo", "401 client error", "access to model", "is restricted"},
		Meaning:  "Hugging Face refuses the model download",
		Hint:     "provide a Hugging Face token Secret for gated or private models",
	},
}

// maxErrorLines bounds how many error lines are shown when no known cause matches.
const maxErrorLines = 3

// ExplainLog returns the first known cause found in the lines, with the line that matched. When nothing known
// matches, it returns the first lines that look like errors, which are usually closer to the cause than the last
// line ("Engine core initialization failed").
func ExplainLog(lines []string) (*LogCause, []string) {
	for _, l := range lines {
		lower := strings.ToLower(l)
		for i := range knownCauses {
			for _, p := range knownCauses[i].Patterns {
				if strings.Contains(lower, p) {
					return &knownCauses[i], []string{strings.TrimSpace(l)}
				}
			}
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, l := range lines {
		t := strings.TrimSpace(l)
		msg := stripPrefix(t)
		lower := strings.ToLower(msg)
		// Skip traceback noise: Python frames, the indented code lines and the ^^^ markers under them.
		if !looksLikeError(lower) || strings.Contains(lower, `file "`) || isTracebackLine(msg) {
			continue
		}
		key := msg
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, t)
		if len(out) == maxErrorLines {
			break
		}
	}
	return nil, out
}

func isTracebackLine(msg string) bool {
	if msg == "" || msg[0] == ' ' || msg[0] == '\t' {
		return true
	}
	return strings.Trim(msg, "^~ ") == ""
}

func looksLikeError(lower string) bool {
	for _, w := range []string{"error:", "error ", "exception", "fatal", "failed"} {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

// stripPrefix drops the prefixes vLLM puts on lines, "(EngineCore pid=7) ERROR 10-29 23:48:18 [core.py:1330] ",
// keeping the indentation of what follows, so repeated messages are recognized and code lines stay indented.
func stripPrefix(line string) string {
	if strings.HasPrefix(line, "(") {
		if i := strings.Index(line, ") "); i > 0 {
			line = line[i+2:]
		}
	}
	if i := strings.Index(line, "] "); i >= 0 && strings.Contains(line[:i], "[") {
		return line[i+2:]
	}
	return line
}

func since(t time.Time) string {
	if t.IsZero() {
		return "an unknown time"
	}
	return duration.HumanDuration(time.Since(t))
}
