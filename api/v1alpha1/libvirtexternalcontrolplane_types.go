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
)

// LibvirtExternalControlPlaneSpec defines the desired state of
// LibvirtExternalControlPlane. It has no fields: the control plane is the one
// CAPLV runs on, and nothing about it is configured here.
type LibvirtExternalControlPlaneSpec struct{}

// LibvirtExternalControlPlaneStatus defines the observed state of
// LibvirtExternalControlPlane, in the shape of the Cluster API v1beta2
// ControlPlane contract.
type LibvirtExternalControlPlaneStatus struct {
	// initialization reports one-time milestones of the control plane, as
	// defined by the Cluster API v1beta2 ControlPlane contract.
	// +optional
	Initialization LibvirtExternalControlPlaneInitializationStatus `json:"initialization,omitempty,omitzero"`

	// externalManagedControlPlane is always true: the control plane's nodes
	// exist, but they are not Cluster API Machines. It tells Cluster API not
	// to look for control plane Machines, for example before it deletes a
	// worker's Node.
	// +optional
	ExternalManagedControlPlane *bool `json:"externalManagedControlPlane,omitempty"`

	// version is the Kubernetes version the control plane serves.
	// +optional
	// +kubebuilder:validation:MaxLength=256
	Version string `json:"version,omitempty"`

	// conditions represent the current state of the LibvirtExternalControlPlane
	// resource. Cluster API mirrors Available into the Cluster's
	// ControlPlaneAvailable condition.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// LibvirtExternalControlPlaneInitializationStatus provides observations of the
// control plane's initialization.
// +kubebuilder:validation:MinProperties=1
type LibvirtExternalControlPlaneInitializationStatus struct {
	// controlPlaneInitialized is true once the control plane accepts requests
	// and Cluster API can reach it through the <cluster>-kubeconfig Secret. It
	// is not set back to false.
	// +optional
	ControlPlaneInitialized *bool `json:"controlPlaneInitialized,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:resource:categories=cluster-api,shortName=libvirtecp
// +kubebuilder:printcolumn:name="Initialized",type="boolean",JSONPath=".status.initialization.controlPlaneInitialized"
// +kubebuilder:printcolumn:name="Available",type="string",JSONPath=".status.conditions[?(@.type=='Available')].status"
// +kubebuilder:printcolumn:name="Version",type="string",JSONPath=".status.version"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// LibvirtExternalControlPlane represents, to Cluster API, a control plane that
// already exists and is not made of Cluster API Machines: the cluster CAPLV
// runs on, such as an OpenShift cluster that CAPLV adds workers to. A Cluster
// names it in spec.controlPlaneRef. It fulfills the Cluster API ControlPlane
// contract, so the Cluster reports its control plane initialized and
// available, which Cluster API needs before it checks node health and runs
// MachineHealthChecks, and it maintains the <cluster>-kubeconfig Secret
// Cluster API uses to reach the cluster.
type LibvirtExternalControlPlane struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of LibvirtExternalControlPlane.
	// +required
	Spec LibvirtExternalControlPlaneSpec `json:"spec"`

	// status defines the observed state of LibvirtExternalControlPlane.
	// +optional
	Status LibvirtExternalControlPlaneStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// LibvirtExternalControlPlaneList contains a list of LibvirtExternalControlPlane.
type LibvirtExternalControlPlaneList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []LibvirtExternalControlPlane `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LibvirtExternalControlPlane{}, &LibvirtExternalControlPlaneList{})
}
