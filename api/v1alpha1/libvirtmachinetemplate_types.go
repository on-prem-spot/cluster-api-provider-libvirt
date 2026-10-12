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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

// LibvirtMachineTemplateResource is the per-machine payload a MachineDeployment
// or KubeadmControlPlane stamps out.
type LibvirtMachineTemplateResource struct {
	// metadata is the object metadata applied to the LibvirtMachines created
	// from this template. Only labels and annotations are propagated.
	// +optional
	ObjectMeta clusterv1.ObjectMeta `json:"metadata,omitempty"`

	// spec is the LibvirtMachine spec stamped onto each created machine.
	// +required
	Spec LibvirtMachineSpec `json:"spec"`
}

// LibvirtMachineTemplateSpec defines the desired state of LibvirtMachineTemplate.
type LibvirtMachineTemplateSpec struct {
	// template is the machine template.
	// +required
	Template LibvirtMachineTemplateResource `json:"template"`
}

// LibvirtMachineTemplateStatus defines the observed state of
// LibvirtMachineTemplate.
//
// Templates are inert -- nothing reconciles them -- so this carries only the
// capacity field the CAPI contract reads for cluster autoscaling.
type LibvirtMachineTemplateStatus struct {
	// capacity is the resource capacity of a machine created from this
	// template, in the form the cluster autoscaler reads (cpu, memory).
	// +optional
	Capacity corev1.ResourceList `json:"capacity,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:resource:categories=cluster-api,shortName=libvirtmt
// +kubebuilder:printcolumn:name="vCPUs",type="integer",JSONPath=".spec.template.spec.domain.vcpus"
// +kubebuilder:printcolumn:name="MemoryMB",type="integer",JSONPath=".spec.template.spec.domain.memoryMB"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// LibvirtMachineTemplate is the Schema for the libvirtmachinetemplates API.
//
// MachineDeployment and KubeadmControlPlane reference a template through
// spec.infrastructureRef and stamp a LibvirtMachine per replica from it. The
// type exists so those higher-order resources can be used at all.
//
// One caveat, because CAPLV's machine model is not replica-shaped: a
// LibvirtMachine names its host through the required spec.hostRef and declares
// a static network identity, and a host runs exactly one VM. Every machine
// stamped from one template therefore carries the same host and the same
// address, so a replica count above one produces machines that collide rather
// than scale. Scaling needs per-machine host and address allocation, which
// CAPLV does not do yet. The validating webhook warns about this on create.
type LibvirtMachineTemplate struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of LibvirtMachineTemplate.
	// +required
	Spec LibvirtMachineTemplateSpec `json:"spec"`

	// status defines the observed state of LibvirtMachineTemplate.
	// +optional
	Status LibvirtMachineTemplateStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// LibvirtMachineTemplateList contains a list of LibvirtMachineTemplate.
type LibvirtMachineTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []LibvirtMachineTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LibvirtMachineTemplate{}, &LibvirtMachineTemplateList{})
}
