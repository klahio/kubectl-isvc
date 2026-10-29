package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/klahio/kubectl-isvc/internal/inference"
	"github.com/klahio/kubectl-isvc/internal/status"
)

type testOptions struct {
	*rootOptions
	filename    string
	protocol    string
	model       string
	url         string
	portForward string
	headers     []string
	timeout     time.Duration
	output      string
}

func newTestCommand(root *rootOptions) *cobra.Command {
	o := &testOptions{rootOptions: root}
	c := &cobra.Command{
		Use:   "test NAME -f INPUT.json",
		Short: "Send one smoke-test inference request, finding the address and protocol automatically",
		Example: fmt.Sprintf(`  %[1]s%[2]s test sklearn-iris -f input.json
  %[1]s%[2]s test sklearn-iris -f input.json -o summary
  cat input.json | %[1]s%[2]s test sklearn-iris -f - -o raw | jq .predictions`, commandPrefix(), PluginName),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return o.run(cmd.Context(), args[0])
		},
	}
	f := c.Flags()
	f.StringVarP(&o.filename, "filename", "f", "", "Request body file; - reads standard input (required)")
	f.StringVar(&o.protocol, "protocol", "", "v1 or v2 (default: automatic)")
	f.StringVar(&o.model, "model", "", "Model name in the request path (default: the InferenceService name)")
	f.StringVar(&o.url, "url", "", "Send to this base address instead of discovering one")
	f.StringVar(&o.portForward, "port-forward", inference.PortForwardAuto, "auto, always or never")
	f.StringArrayVarP(&o.headers, "header", "H", nil, `Extra request header "Name: value", repeatable`)
	f.DurationVar(&o.timeout, "timeout", 30*time.Second, "Request timeout")
	f.StringVarP(&o.output, "output", "o", inference.OutputPretty, "Output: "+strings.Join(inference.Outputs, ", "))
	_ = c.MarkFlagRequired("filename")
	return c
}

func usage(format string, args ...any) error {
	return &ExitError{Code: ExitUsage, Err: fmt.Errorf(format, args...)}
}

func (o *testOptions) run(ctx context.Context, name string) error {
	if !slices.Contains(inference.Outputs, o.output) {
		return usage("unknown output format %q (use %s)", o.output, strings.Join(inference.Outputs, ", "))
	}
	switch o.portForward {
	case inference.PortForwardAuto, inference.PortForwardAlways, inference.PortForwardNever:
	default:
		return usage("--port-forward must be auto, always or never, not %q", o.portForward)
	}
	if o.url != "" && o.portForward == inference.PortForwardAlways {
		return usage("--url and --port-forward=always cannot be used together")
	}
	headers, err := inference.ParseHeaders(o.headers)
	if err != nil {
		return usage("%v", err)
	}
	body, err := o.readInput()
	if err != nil {
		return usage("%v", err)
	}

	if ctx == nil {
		ctx = context.Background()
	}
	clients, namespace, err := o.clients()
	if err != nil {
		return err
	}
	snap, err := status.Collect(ctx, clients, namespace, name, false)
	if err != nil {
		return clusterError(err)
	}
	summary := status.Summarize(snap, status.Diagnose(snap))
	if summary.Ready != "True" {
		fmt.Fprintf(o.streams.ErrOut, "warning: %s/%s is not Ready; `%s%s status %s` shows why\n", namespace, name, commandPrefix(), PluginName, name)
	}

	choice, err := inference.ChooseProtocol(o.protocol, snap.ISVC, snap.Runtime, body)
	if err != nil {
		return usage("%v", err)
	}
	model := o.model
	if model == "" {
		model = name
	}

	ep, err := inference.Discover(ctx, clients, summary, snap, inference.DiscoverOptions{URL: o.url, PortForward: o.portForward})
	if err != nil {
		return &ExitError{Code: ExitFailure, Err: err}
	}
	defer ep.Close()

	if err := inference.CheckPath(ctx, ep, choice.Protocol); err != nil {
		return &ExitError{Code: ExitUsage, Err: err}
	}
	res, err := inference.Send(ctx, inference.Request{
		Endpoint: ep,
		Protocol: choice,
		Model:    model,
		Body:     body,
		Headers:  headers,
		Timeout:  o.timeout,
	})
	if err != nil {
		return &ExitError{Code: ExitFailure, Err: err}
	}
	if err := inference.Write(o.streams.Out, o.output, res); err != nil {
		return err
	}
	if !res.OK() {
		return &ExitError{Code: ExitFailure}
	}
	return nil
}

func (o *testOptions) readInput() ([]byte, error) {
	var (
		data []byte
		err  error
	)
	if o.filename == "-" {
		data, err = io.ReadAll(o.streams.In)
	} else {
		data, err = os.ReadFile(o.filename)
	}
	if err != nil {
		return nil, err
	}
	if !json.Valid(data) {
		return nil, errors.New("the input is not valid JSON")
	}
	return data, nil
}
