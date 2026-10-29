package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/duration"
	"sigs.k8s.io/yaml"

	"github.com/klahio/kubectl-kserve/internal/kube"
	"github.com/klahio/kubectl-kserve/internal/status"
)

const (
	statusLogLines     = 5
	statusWatchPeriod  = 2 * time.Second
	clearScreenAndHome = "\033[H\033[2J"
)

type statusOptions struct {
	*rootOptions
	output   string
	events   bool
	watch    bool
	exitCode bool
}

func newStatusCommand(root *rootOptions) *cobra.Command {
	o := &statusOptions{rootOptions: root}
	c := &cobra.Command{
		Use:   "status [isvc/]NAME",
		Short: "Show a one-screen summary of an InferenceService and diagnose why it is not working",
		Example: fmt.Sprintf(`  %[1]s%[2]s status sklearn-iris -n kserve-test
  %[1]s%[2]s status sklearn-iris -o json
  %[1]s%[2]s status sklearn-iris --exit-code   # exit 1 when not Ready`, commandPrefix(), PluginName),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := inferenceServiceName(args[0])
			if err != nil {
				return err
			}
			return o.run(cmd.Context(), name)
		},
	}
	c.Flags().StringVarP(&o.output, "output", "o", "", "Output format: json or yaml (a structured summary). Default is human-readable")
	c.Flags().BoolVar(&o.events, "events", false, "Also list recent events of the InferenceService and its pods")
	c.Flags().BoolVarP(&o.watch, "watch", "w", false, "Redraw every 2 seconds until interrupted")
	c.Flags().BoolVar(&o.exitCode, "exit-code", false, "Exit with code 1 when the InferenceService is not Ready")
	return c
}

func (o *statusOptions) run(ctx context.Context, name string) error {
	switch o.output {
	case "", "json", "yaml":
	default:
		return &ExitError{Code: ExitUsage, Err: fmt.Errorf("unknown output format %q (use json or yaml)", o.output)}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	clients, namespace, err := o.clients()
	if err != nil {
		return err
	}

	if !o.watch {
		ready, err := o.once(ctx, clients, namespace, name, o.streams.Out)
		if err != nil {
			return err
		}
		return o.result(ready)
	}

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	clear := o.useColor(o.streams.Out) && o.output == ""
	ready := false
	for {
		var buf bytes.Buffer
		r, err := o.once(ctx, clients, namespace, name, &buf)
		if err != nil && ctx.Err() == nil {
			return err
		}
		if ctx.Err() != nil {
			return o.result(ready)
		}
		ready = r
		if clear {
			fmt.Fprint(o.streams.Out, clearScreenAndHome)
		}
		fmt.Fprint(o.streams.Out, buf.String())
		if !clear {
			fmt.Fprintln(o.streams.Out, "---")
		}
		select {
		case <-ctx.Done():
			return o.result(ready)
		case <-time.After(statusWatchPeriod):
		}
	}
}

// result turns the Ready state into the exit code: informational by default, health check with --exit-code.
func (o *statusOptions) result(ready bool) error {
	if o.exitCode && !ready {
		return &ExitError{Code: ExitFailure}
	}
	return nil
}

// once collects, diagnoses and prints one summary. It reports whether the InferenceService is Ready.
func (o *statusOptions) once(ctx context.Context, clients kube.Clients, namespace, name string, w io.Writer) (bool, error) {
	snap, err := status.Collect(ctx, clients, namespace, name, o.events)
	if err != nil {
		return false, clusterError(err)
	}
	if !snap.ISVC.ModeSupported() && snap.ISVC.Mode != "" {
		return false, &ExitError{Code: ExitFailure, Err: fmt.Errorf(
			"InferenceService %s/%s uses deployment mode %q; version 0.1 supports only Standard mode", namespace, name, snap.ISVC.Mode)}
	}

	findings := status.Diagnose(snap)
	for i := range findings {
		if ref := findings[i].Logs; ref != nil {
			findings[i].LogLines = tailLogs(ctx, clients, namespace, ref)
		}
	}
	summary := status.Summarize(snap, findings)
	events := eventLines(snap.Events)
	ready := summary.Ready == "True"

	switch o.output {
	case "json", "yaml":
		doc := struct {
			*status.Summary
			Events []status.EventLine `json:"events,omitempty"`
		}{summary, events}
		data, err := marshal(o.output, doc)
		if err != nil {
			return ready, err
		}
		_, err = w.Write(data)
		return ready, err
	}

	r := status.TextRenderer{Color: o.useColor(o.streams.Out)}
	if err := r.Render(w, summary); err != nil {
		return ready, err
	}
	if o.events {
		if err := r.RenderEvents(w, events); err != nil {
			return ready, err
		}
	}
	return ready, nil
}

func tailLogs(ctx context.Context, clients kube.Clients, namespace string, ref *status.LogRef) []string {
	lines := int64(statusLogLines)
	req := clients.Kube.CoreV1().Pods(namespace).GetLogs(ref.Pod, &corev1.PodLogOptions{
		Container: ref.Container,
		Previous:  ref.Previous,
		TailLines: &lines,
	})
	data, err := req.DoRaw(ctx)
	if err != nil && ref.Previous {
		// The previous instance may already be gone; the current one is the next best evidence.
		ref.Previous = false
		req = clients.Kube.CoreV1().Pods(namespace).GetLogs(ref.Pod, &corev1.PodLogOptions{Container: ref.Container, TailLines: &lines})
		data, err = req.DoRaw(ctx)
	}
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, truncate(l, 200))
		}
	}
	return out
}

func eventLines(events []corev1.Event) []status.EventLine {
	var out []status.EventLine
	now := time.Now()
	for _, e := range events {
		if status.IsNoise(e) {
			continue
		}
		t := e.LastTimestamp.Time
		if t.IsZero() {
			t = e.EventTime.Time
		}
		age := "-"
		if !t.IsZero() {
			age = duration.HumanDuration(now.Sub(t))
		}
		out = append(out, status.EventLine{
			Age:     age,
			Type:    e.Type,
			Reason:  e.Reason,
			Object:  strings.ToLower(e.InvolvedObject.Kind) + "/" + e.InvolvedObject.Name,
			Message: truncate(strings.TrimSpace(e.Message), 160),
		})
	}
	return out
}

func marshal(format string, v any) ([]byte, error) {
	data, err := yaml.Marshal(v)
	if err != nil || format == "yaml" {
		return data, err
	}
	data, err = yaml.YAMLToJSON(data)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := jsonIndent(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func clusterError(err error) error {
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		return err
	}
	return &ExitError{Code: ExitClusterOrNF, Err: err}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
