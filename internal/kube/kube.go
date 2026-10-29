// Package kube holds the Kubernetes clients shared by all commands and the lookups they have in common.
package kube

import (
	"context"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/klahio/kubectl-isvc/internal/isvc"
)

// Clients are the Kubernetes clients the commands read from.
type Clients struct {
	Kube    kubernetes.Interface
	Dynamic dynamic.Interface
}

// ErrNotFound means the InferenceService does not exist.
var ErrNotFound = errors.New("InferenceService not found")

// GetInferenceService reads one InferenceService; a missing one is reported as ErrNotFound.
func (c Clients) GetInferenceService(ctx context.Context, namespace, name string) (*unstructured.Unstructured, error) {
	u, err := c.Dynamic.Resource(isvc.InferenceServiceGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("%w: %s/%s", ErrNotFound, namespace, name)
	}
	return u, err
}
