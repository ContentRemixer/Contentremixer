package writer

import (
	"context"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

// K8sStyleLookup resolves ContentStyle CRs via the operator's cached client
// (ISI-7976 W2). Wire with the same mgr.GetClient() used by the webhooks so
// the cache is shared and no extra API calls are made in steady state.
type K8sStyleLookup struct {
	Client client.Client
}

var _ StyleLookup = (*K8sStyleLookup)(nil)

// LookupStyle lists ContentStyle CRs in the given namespace and returns the
// Guide for the requested outputFormat. Returns ("", nil) when no matching CR
// exists (the writer falls back to the repo file). Returns a non-nil error
// only on a hard client/cache failure.
func (k *K8sStyleLookup) LookupStyle(ctx context.Context, namespace, outputFormat string) (string, error) {
	var list contentremixerv1alpha1.ContentStyleList
	opts := []client.ListOption{}
	if ns := strings.TrimSpace(namespace); ns != "" {
		opts = append(opts, client.InNamespace(ns))
	}
	if err := k.Client.List(ctx, &list, opts...); err != nil {
		return "", err
	}
	for _, cs := range list.Items {
		if strings.EqualFold(cs.Spec.OutputFormat, outputFormat) {
			guide := strings.TrimSpace(cs.Spec.Guide)
			if guide != "" {
				return guide, nil
			}
		}
	}
	return "", nil
}
