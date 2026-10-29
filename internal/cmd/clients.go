package cmd

import (
	"bytes"
	"encoding/json"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/klahio/kubectl-isvc/internal/status"
)

// clients builds the Kubernetes clients and resolves the namespace from the kubectl-compatible flags.
func (o *rootOptions) clients() (status.Clients, string, error) {
	namespace, _, err := o.configFlags.ToRawKubeConfigLoader().Namespace()
	if err != nil {
		return status.Clients{}, "", &ExitError{Code: ExitClusterOrNF, Err: err}
	}
	cfg, err := o.configFlags.ToRESTConfig()
	if err != nil {
		return status.Clients{}, "", &ExitError{Code: ExitClusterOrNF, Err: err}
	}
	kube, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return status.Clients{}, "", &ExitError{Code: ExitClusterOrNF, Err: err}
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return status.Clients{}, "", &ExitError{Code: ExitClusterOrNF, Err: err}
	}
	return status.Clients{Kube: kube, Dynamic: dyn}, namespace, nil
}

func jsonIndent(buf *bytes.Buffer, data []byte) error {
	if err := json.Indent(buf, data, "", "  "); err != nil {
		return err
	}
	buf.WriteByte('\n')
	return nil
}
