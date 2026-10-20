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

package v1alpha1

import (
	"fmt"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// LibvirtClusterIdentitySpec defines the SSH credential an identity holds and
// the namespaces whose LibvirtHosts may use it.
type LibvirtClusterIdentitySpec struct {
	// secretName is the name of a Secret in the controller's namespace holding
	// the SSH private key under the `ssh-privatekey` key, the same shape as a
	// LibvirtHost's spec.secretRef Secret. The controller reads it on every
	// connection, so replacing the key in the Secret, or pointing secretName
	// at a new Secret, rotates the credential for every host using this
	// identity without editing the hosts.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	SecretName string `json:"secretName"`

	// allowedNamespaces selects the namespaces whose LibvirtHosts may use
	// this identity. A namespace is allowed if it is in list or matches
	// selector. An empty allowedNamespaces ({}) allows every namespace.
	// When unset, no namespace may use the identity.
	// +optional
	AllowedNamespaces *AllowedNamespaces `json:"allowedNamespaces,omitempty"`
}

// AllowedNamespaces selects namespaces by name or by label.
type AllowedNamespaces struct {
	// list names the allowed namespaces.
	// +optional
	// +listType=set
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=63
	List []string `json:"list,omitempty"`

	// selector selects allowed namespaces by their labels. An empty selector
	// ({}) matches every namespace.
	// +optional
	Selector *metav1.LabelSelector `json:"selector,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,categories=cluster-api,shortName=libvirtci
// +kubebuilder:printcolumn:name="Secret",type="string",JSONPath=".spec.secretName"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// LibvirtClusterIdentity is a cluster-scoped SSH credential that LibvirtHosts
// in the namespaces it allows can reference with spec.identityRef instead of
// carrying their own spec.secretRef. Its Secret lives in the controller's
// namespace, so the authors of those hosts use the credential without being
// able to read it.
type LibvirtClusterIdentity struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of LibvirtClusterIdentity
	// +required
	Spec LibvirtClusterIdentitySpec `json:"spec"`
}

// +kubebuilder:object:root=true

// LibvirtClusterIdentityList contains a list of LibvirtClusterIdentity
type LibvirtClusterIdentityList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []LibvirtClusterIdentity `json:"items"`
}

// NeedsNamespaceLabels reports whether AllowsNamespace consults namespace
// labels, so callers can skip fetching the Namespace when it does not.
func (i *LibvirtClusterIdentity) NeedsNamespaceLabels() bool {
	return i.Spec.AllowedNamespaces != nil && i.Spec.AllowedNamespaces.Selector != nil
}

// AllowsNamespace reports whether spec.allowedNamespaces admits the namespace
// with the given name and labels. Unset admits nothing; an empty struct
// admits everything.
func (i *LibvirtClusterIdentity) AllowsNamespace(name string, nsLabels map[string]string) (bool, error) {
	allowed := i.Spec.AllowedNamespaces
	switch {
	case allowed == nil:
		return false, nil
	case len(allowed.List) == 0 && allowed.Selector == nil:
		return true, nil
	case slices.Contains(allowed.List, name):
		return true, nil
	case allowed.Selector == nil:
		return false, nil
	}
	selector, err := metav1.LabelSelectorAsSelector(allowed.Selector)
	if err != nil {
		return false, fmt.Errorf("LibvirtClusterIdentity %s has an invalid spec.allowedNamespaces.selector: %w", i.Name, err)
	}
	return selector.Matches(labels.Set(nsLabels)), nil
}

func init() {
	SchemeBuilder.Register(&LibvirtClusterIdentity{}, &LibvirtClusterIdentityList{})
}
