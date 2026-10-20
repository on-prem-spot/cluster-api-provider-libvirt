/*
Copyright 2026 Anthony Green.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

// hostSSHSecret returns the Secret holding host's SSH private key, either
// the one spec.secretRef names in the host's namespace or the one held by the
// LibvirtClusterIdentity spec.identityRef names. identityNamespace is the
// controller's namespace, where identity Secrets live.
func hostSSHSecret(ctx context.Context, c client.Client, host *infrav1.LibvirtHost, identityNamespace string) (*corev1.Secret, error) {
	switch {
	case host.Spec.IdentityRef != nil:
		return identitySSHSecret(ctx, c, host, identityNamespace)
	case host.Spec.SecretRef != nil:
		// Confined to the host's own namespace; see resolveSecretNamespace.
		secretNS, err := resolveSecretNamespace(host.Spec.SecretRef.Namespace, host.Namespace, "spec.secretRef.namespace")
		if err != nil {
			return nil, err
		}
		secret := &corev1.Secret{}
		key := types.NamespacedName{Namespace: secretNS, Name: host.Spec.SecretRef.Name}
		if err := c.Get(ctx, key, secret); err != nil {
			return nil, fmt.Errorf("failed to get SSH secret %s: %w", key, err)
		}
		return secret, nil
	default:
		return nil, fmt.Errorf("LibvirtHost %s/%s sets neither spec.secretRef nor spec.identityRef", host.Namespace, host.Name)
	}
}

func identitySSHSecret(ctx context.Context, c client.Client, host *infrav1.LibvirtHost, identityNamespace string) (*corev1.Secret, error) {
	name := host.Spec.IdentityRef.Name
	identity := &infrav1.LibvirtClusterIdentity{}
	if err := c.Get(ctx, types.NamespacedName{Name: name}, identity); err != nil {
		return nil, fmt.Errorf("failed to get LibvirtClusterIdentity %s: %w", name, err)
	}
	allowed, err := identityAllowsNamespace(ctx, c, identity, host.Namespace)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, fmt.Errorf("LibvirtClusterIdentity %s does not allow namespace %q in spec.allowedNamespaces", name, host.Namespace)
	}
	if identityNamespace == "" {
		return nil, fmt.Errorf("cannot resolve LibvirtClusterIdentity %s: the controller's namespace is unknown "+
			"(set POD_NAMESPACE or --identity-namespace)", name)
	}
	secret := &corev1.Secret{}
	key := types.NamespacedName{Namespace: identityNamespace, Name: identity.Spec.SecretName}
	if err := c.Get(ctx, key, secret); err != nil {
		return nil, fmt.Errorf("failed to get SSH secret %s of LibvirtClusterIdentity %s: %w", key, name, err)
	}
	if err := ensureIdentityOwnsSecret(ctx, c, identity, secret); err != nil {
		// Ownership only matters to clusterctl move; never block a
		// connection on it.
		logf.FromContext(ctx).Error(err, "Failed to set owner reference on identity Secret",
			"identity", name, "secret", key)
	}
	return secret, nil
}

// ensureIdentityOwnsSecret makes identity an owner of its Secret, so that
// clusterctl move carries the Secret along with the identity and, because the
// identity is cluster-scoped, leaves it in place on the source cluster too.
// The reference is not a controller reference, so a Secret shared by several
// identities keeps one per identity.
func ensureIdentityOwnsSecret(ctx context.Context, c client.Client, identity *infrav1.LibvirtClusterIdentity, secret *corev1.Secret) error {
	if owned, err := controllerutil.HasOwnerReference(secret.OwnerReferences, identity, c.Scheme()); err != nil || owned {
		return err
	}
	patch := client.MergeFrom(secret.DeepCopy())
	if err := controllerutil.SetOwnerReference(identity, secret, c.Scheme()); err != nil {
		return err
	}
	return c.Patch(ctx, secret, patch)
}

// identityAllowsNamespace reports whether identity admits namespace,
// fetching the Namespace only when a label selector needs its labels.
func identityAllowsNamespace(ctx context.Context, c client.Client, identity *infrav1.LibvirtClusterIdentity, namespace string) (bool, error) {
	var nsLabels map[string]string
	if identity.NeedsNamespaceLabels() {
		ns := &corev1.Namespace{}
		if err := c.Get(ctx, types.NamespacedName{Name: namespace}, ns); err != nil {
			return false, fmt.Errorf("failed to get Namespace %s: %w", namespace, err)
		}
		nsLabels = ns.Labels
	}
	return identity.AllowsNamespace(namespace, nsLabels)
}
