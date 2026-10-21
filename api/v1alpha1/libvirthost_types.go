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

// SecretReference is a reference to a Kubernetes Secret.
type SecretReference struct {
	// name is the name of the Secret.
	// +required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// namespace is the namespace of the Secret. Deprecated: the Secret must
	// live in the same namespace as the resource that references it, so the
	// only accepted values are "" and that namespace. Naming any other
	// namespace is rejected — the controller reads Secrets with cluster-wide
	// RBAC, so honouring an arbitrary namespace here would let the author of a
	// namespaced resource borrow a Secret they cannot read themselves.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// LibvirtClusterIdentityReference names a LibvirtClusterIdentity.
type LibvirtClusterIdentityReference struct {
	// name is the name of the LibvirtClusterIdentity.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
}

// LibvirtMachineReference names a LibvirtMachine in the referrer's namespace.
type LibvirtMachineReference struct {
	// name is the name of the LibvirtMachine.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
}

// MachineNetworkSpec is a LibvirtHost's network configuration for its VM.
// It has the fields of a LibvirtMachine's spec.network except model, which
// defaults on the machine, and macAddress.
type MachineNetworkSpec struct {
	// type is the type of libvirt network attachment.
	// +optional
	Type NetworkType `json:"type,omitempty"`

	// name is the name of the libvirt network or bridge device.
	// +optional
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name,omitempty"`

	// addresses is a list of static IP addresses in CIDR notation (e.g. 192.168.1.10/24).
	// +optional
	Addresses []string `json:"addresses,omitempty"`

	// gateway is the default gateway address.
	// +optional
	Gateway string `json:"gateway,omitempty"`

	// dns defines DNS configuration for the network.
	// +optional
	DNS *DNSSpec `json:"dns,omitempty"`
}

// LibvirtHostSpec defines the desired state of LibvirtHost.
// +kubebuilder:validation:XValidation:rule="has(self.secretRef) != has(self.identityRef)",message="exactly one of secretRef and identityRef must be set"
type LibvirtHostSpec struct {
	// uri is the libvirt connection URI (e.g. qemu+ssh://user@host/system).
	// The path selects the libvirt daemon on the host: /system (default)
	// connects to the privileged system daemon; /session connects to the
	// per-user daemon of the SSH user. Session mode requires host setup for
	// unprivileged networking (qemu-bridge-helper) and only supports
	// network.type=bridge on machines.
	// +required
	// +kubebuilder:validation:MinLength=1
	URI string `json:"uri"`

	// secretRef is a reference to a Secret in the host's namespace containing
	// the SSH private key for authenticating to the libvirt host, under the
	// `ssh-privatekey` key. Exactly one of secretRef and identityRef must be
	// set.
	// +optional
	SecretRef *SecretReference `json:"secretRef,omitempty"`

	// identityRef names a cluster-scoped LibvirtClusterIdentity holding the
	// SSH private key, for credentials shared across hosts and namespaces
	// and rotated in one place. The identity must allow this host's
	// namespace. Exactly one of secretRef and identityRef must be set.
	// +optional
	IdentityRef *LibvirtClusterIdentityReference `json:"identityRef,omitempty"`

	// hostKeyFingerprint is the SSH host key fingerprint (SHA256:...) used to
	// verify the identity of the remote host.
	// +optional
	HostKeyFingerprint string `json:"hostKeyFingerprint,omitempty"`

	// firmwarePath overrides the OVMF firmware path on the host.
	// +optional
	// +kubebuilder:default="/usr/share/OVMF/OVMF_CODE.secboot.fd"
	FirmwarePath string `json:"firmwarePath,omitempty"`

	// nvramTemplatePath is the UEFI VARS template on the host that each
	// machine's private NVRAM file is initialized from (the domain XML's
	// <nvram template=...>). It should be the VARS file matching the
	// firmwarePath loader build.
	// +optional
	// +kubebuilder:default="/usr/share/OVMF/OVMF_VARS.fd"
	NVRAMTemplatePath string `json:"nvramTemplatePath,omitempty"`

	// reservedResources specifies how much CPU and memory to reserve for the
	// host's incumbent workloads. CAPLV will not allocate VMs that exceed
	// the remaining capacity. If unset, defaults to reserving 2 vCPUs and
	// 4096 MB of memory.
	// +optional
	ReservedResources *ReservedResources `json:"reservedResources,omitempty"`

	// healthCheckIntervalSeconds is how often (in seconds) to recheck this
	// host when it has active machines. Hosts with no machines are not polled.
	// Default: 300 (5 minutes).
	// +optional
	// +kubebuilder:default=300
	// +kubebuilder:validation:Minimum=30
	HealthCheckIntervalSeconds int32 `json:"healthCheckIntervalSeconds,omitempty"`

	// machineNetwork is the network configuration for the VM this host runs.
	// A host runs one VM, so its address can belong to the host, which keeps
	// it valid on the host's network. A LibvirtMachine on this host takes
	// each field it leaves unset in spec.network from here.
	// +optional
	MachineNetwork *MachineNetworkSpec `json:"machineNetwork,omitempty"`

	// unschedulable cordons the host, as a Node's spec.unschedulable does:
	// CAPLV gives it to no machine that does not already hold it, offers it
	// to the control plane as no failure domain, and creates or starts no VM
	// on it, including a VM that stopped. A VM already running there keeps
	// running. Set it while the host's capacity belongs to something else,
	// for example while it runs a failed-over database; a VM stopped then
	// (virsh destroy) stays stopped until the host is uncordoned.
	// +optional
	Unschedulable bool `json:"unschedulable,omitempty"`

	// consumerRef names the LibvirtMachine using this host. CAPLV sets it
	// when a machine claims the host and clears it once the machine's VM and
	// artifacts are deleted; it is what keeps a host to one VM. Clearing it
	// by hand hands the host to another machine while the old VM may still
	// exist.
	// +optional
	ConsumerRef *LibvirtMachineReference `json:"consumerRef,omitempty"`
}

// ReservedResources defines resources reserved for the host's incumbent workloads.
type ReservedResources struct {
	// vcpus is the number of vCPUs to reserve for the host. Default: 2.
	// +optional
	// +kubebuilder:default=2
	// +kubebuilder:validation:Minimum=0
	VCPUs int32 `json:"vcpus,omitempty"`

	// memoryMB is the amount of memory in MB to reserve for the host. Default: 4096.
	// +optional
	// +kubebuilder:default=4096
	// +kubebuilder:validation:Minimum=0
	MemoryMB int32 `json:"memoryMB,omitempty"`
}

// HostCapacity reports the discovered and available resources on a libvirt host.
type HostCapacity struct {
	// totalVCPUs is the total number of CPU threads on the host.
	// +optional
	TotalVCPUs int32 `json:"totalVCPUs,omitempty"`

	// totalMemoryMB is the total physical memory on the host in MB.
	// +optional
	TotalMemoryMB int32 `json:"totalMemoryMB,omitempty"`

	// availableVCPUs is the vCPUs available for CAPLV after reservations.
	// +optional
	AvailableVCPUs int32 `json:"availableVCPUs,omitempty"`

	// availableMemoryMB is the memory available for CAPLV after reservations in MB.
	// +optional
	AvailableMemoryMB int32 `json:"availableMemoryMB,omitempty"`
}

// LibvirtHostStatus defines the observed state of LibvirtHost.
type LibvirtHostStatus struct {
	// ready indicates whether the host is reachable and usable.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// capacity reports the discovered host resources and what is available
	// for CAPLV after reservations.
	// +optional
	Capacity *HostCapacity `json:"capacity,omitempty"`

	// lastChecked is the timestamp of the last connectivity check.
	// +optional
	LastChecked *metav1.Time `json:"lastChecked,omitempty"`

	// conditions represent the current state of the LibvirtHost resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:resource:categories=cluster-api,shortName=libvirth
// +kubebuilder:printcolumn:name="URI",type="string",JSONPath=".spec.uri"
// +kubebuilder:printcolumn:name="Ready",type="boolean",JSONPath=".status.ready"
// +kubebuilder:printcolumn:name="Cordoned",type="boolean",JSONPath=".spec.unschedulable"
// +kubebuilder:printcolumn:name="Consumer",type="string",JSONPath=".spec.consumerRef.name"
// +kubebuilder:printcolumn:name="vCPUs",type="integer",JSONPath=".status.capacity.availableVCPUs",priority=1
// +kubebuilder:printcolumn:name="MemMB",type="integer",JSONPath=".status.capacity.availableMemoryMB",priority=1
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// LibvirtHost is the Schema for the libvirthosts API.
// It represents a reusable libvirt host connection configuration.
type LibvirtHost struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of LibvirtHost.
	// +required
	Spec LibvirtHostSpec `json:"spec"`

	// status defines the observed state of LibvirtHost.
	// +optional
	Status LibvirtHostStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// LibvirtHostList contains a list of LibvirtHost.
type LibvirtHostList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []LibvirtHost `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LibvirtHost{}, &LibvirtHostList{})
}
