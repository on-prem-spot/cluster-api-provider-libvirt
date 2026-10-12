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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

// LibvirtClusterTemplateResource is the per-cluster payload a ClusterClass
// stamps out.
type LibvirtClusterTemplateResource struct {
	// metadata is the object metadata applied to the LibvirtClusters created
	// from this template. Only labels and annotations are propagated.
	// +optional
	ObjectMeta clusterv1.ObjectMeta `json:"metadata,omitempty"`

	// spec is the LibvirtCluster spec stamped onto each created cluster.
	// +required
	Spec LibvirtClusterSpec `json:"spec"`
}

// LibvirtClusterTemplateSpec defines the desired state of
// LibvirtClusterTemplate.
type LibvirtClusterTemplateSpec struct {
	// template is the cluster template.
	// +required
	Template LibvirtClusterTemplateResource `json:"template"`
}

// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:resource:categories=cluster-api,shortName=libvirtct
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// LibvirtClusterTemplate is the Schema for the libvirtclustertemplates API.
//
// A ClusterClass references this through spec.infrastructure.ref and stamps a
// LibvirtCluster from it per cluster in the topology. The type exists so
// ClusterClass can name a CAPLV infrastructure template at all.
//
// Templates are inert: nothing reconciles them, so there is no status.
//
// spec.template.spec.controlPlaneEndpoint should normally be left unset here.
// Each cluster has its own endpoint, and a value baked into the template is
// copied onto every cluster created from it. A stamped cluster with no
// endpoint reports Ready=false with reason WaitingForControlPlaneEndpoint
// until one is set, which is the intended path; the validating webhook warns
// when a template carries one.
type LibvirtClusterTemplate struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of LibvirtClusterTemplate.
	// +required
	Spec LibvirtClusterTemplateSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// LibvirtClusterTemplateList contains a list of LibvirtClusterTemplate.
type LibvirtClusterTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []LibvirtClusterTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LibvirtClusterTemplate{}, &LibvirtClusterTemplateList{})
}
