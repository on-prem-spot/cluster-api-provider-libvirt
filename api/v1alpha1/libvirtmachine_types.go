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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

// BootstrapFormat defines the format of bootstrap data.
// +kubebuilder:validation:Enum=ignition;cloud-init
type BootstrapFormat string

const (
	BootstrapFormatIgnition  BootstrapFormat = "ignition"
	BootstrapFormatCloudInit BootstrapFormat = "cloud-init"
)

// NetworkType defines the type of libvirt network attachment.
// +kubebuilder:validation:Enum=bridge;network
type NetworkType string

const (
	NetworkTypeBridge  NetworkType = "bridge"
	NetworkTypeNetwork NetworkType = "network"
)

// CloneStrategy defines how the root disk is cloned from the base image.
// +kubebuilder:validation:Enum=copy-on-write;full-clone
type CloneStrategy string

const (
	CloneStrategyCopyOnWrite CloneStrategy = "copy-on-write"
	CloneStrategyFullClone   CloneStrategy = "full-clone"
)

// Firmware defines the firmware type for the virtual machine.
// +kubebuilder:validation:Enum=uefi;bios
type Firmware string

const (
	FirmwareUEFI Firmware = "uefi"
	FirmwareBIOS Firmware = "bios"
)

// DomainSpec defines the virtual machine domain configuration.
type DomainSpec struct {
	// vcpus is the number of virtual CPUs to allocate.
	// If zero or omitted, auto-sized from the LibvirtHost's available
	// capacity (total vCPUs minus reserved).
	// +optional
	// +kubebuilder:validation:Minimum=0
	VCPUs int32 `json:"vcpus,omitempty"`

	// memoryMB is the amount of memory in megabytes to allocate.
	// If zero or omitted, auto-sized from the LibvirtHost's available
	// capacity (total memory minus reserved).
	// +optional
	// +kubebuilder:validation:Minimum=0
	MemoryMB int32 `json:"memoryMB,omitempty"`

	// machine is the QEMU machine type.
	// +optional
	// +kubebuilder:default="q35"
	Machine string `json:"machine,omitempty"`

	// firmware is the firmware type for the virtual machine.
	// +optional
	// +kubebuilder:default="uefi"
	Firmware Firmware `json:"firmware,omitempty"`
}

// RootDiskSpec defines the root disk configuration.
type RootDiskSpec struct {
	// size is the desired size of the root disk.
	// +required
	Size resource.Quantity `json:"size"`

	// storagePool is the name of the pre-existing libvirt storage pool
	// that receives the root disk volume (and the NoCloud ISO for
	// cloud-init guests). Not used as the disk target when ephemeralPool
	// is true — CAPLV then provisions its own per-machine tmpfs pool
	// instead — though baseImagePool still defaults to this value when
	// unset.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.:-]+$`
	StoragePool string `json:"storagePool"`

	// baseImage is the name of the base image volume to clone from.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.:-]+$`
	BaseImage string `json:"baseImage"`

	// baseImagePool is the storage pool containing the base image.
	// If unset, defaults to storagePool. This allows the base image to
	// live on persistent storage while ephemeral VM disks use a different
	// pool (e.g., tmpfs-backed for ephemeral workers).
	// +optional
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.:-]+$`
	BaseImagePool string `json:"baseImagePool,omitempty"`

	// ephemeralPool, when true, causes CAPLV to create a per-machine
	// tmpfs-backed pool named <namespace>-<cluster>-<machine>-pool
	// (surfaced in status.artifacts.ephemeralPoolName) at provisioning
	// time and destroy it, including the tmpfs mount, at deletion time.
	// The root disk and any bootstrap ISO are placed in this generated
	// pool instead of storagePool. This ensures the host's persistent
	// storage is never touched and RAM is only consumed while the VM
	// exists. Set baseImagePool to the persistent pool holding the base
	// image.
	// +optional
	EphemeralPool bool `json:"ephemeralPool,omitempty"`

	// ephemeralPoolSize caps the RAM the ephemeral tmpfs mount may consume.
	// Accepts tmpfs `size=` syntax: a percentage of physical RAM (e.g. "80%")
	// or an absolute size (e.g. "16G"). When unset, the kernel default (50%
	// of physical RAM) applies. Only meaningful when ephemeralPool is true.
	// +optional
	// +kubebuilder:validation:Pattern=`^([1-9][0-9]?|100)%$|^[1-9][0-9]*[KMGTkmgt]?$`
	EphemeralPoolSize string `json:"ephemeralPoolSize,omitempty"`

	// bus is the disk bus type.
	// +optional
	// +kubebuilder:default="virtio"
	Bus string `json:"bus,omitempty"`

	// cloneStrategy defines how the root disk is cloned from the base image.
	// +optional
	// +kubebuilder:default="copy-on-write"
	CloneStrategy CloneStrategy `json:"cloneStrategy,omitempty"`
}

// AdditionalDiskSpec defines an additional data disk.
type AdditionalDiskSpec struct {
	// name is a unique identifier for this disk within the machine.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.:-]+$`
	Name string `json:"name"`

	// size is the desired size of the disk.
	// +required
	Size resource.Quantity `json:"size"`

	// storagePool is the name of the libvirt storage pool to use.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.:-]+$`
	StoragePool string `json:"storagePool"`

	// bus is the disk bus type.
	// +optional
	// +kubebuilder:default="virtio"
	Bus string `json:"bus,omitempty"`
}

// DNSSpec defines DNS configuration for the network.
type DNSSpec struct {
	// nameservers is a list of DNS server addresses.
	// +optional
	Nameservers []string `json:"nameservers,omitempty"`

	// searchDomains is a list of DNS search domains.
	// +optional
	SearchDomains []string `json:"searchDomains,omitempty"`
}

// NetworkSpec defines the network configuration for the virtual machine.
type NetworkSpec struct {
	// type is the type of libvirt network attachment.
	// +required
	Type NetworkType `json:"type"`

	// name is the name of the libvirt network or bridge device.
	// +required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// model is the NIC model.
	// +optional
	// +kubebuilder:default="virtio"
	Model string `json:"model,omitempty"`

	// addresses is a list of static IP addresses in CIDR notation (e.g. 192.168.1.10/24).
	// +required
	// +kubebuilder:validation:MinItems=1
	Addresses []string `json:"addresses"`

	// gateway is the default gateway address.
	// +optional
	Gateway string `json:"gateway,omitempty"`

	// dns defines DNS configuration for the network.
	// +optional
	DNS *DNSSpec `json:"dns,omitempty"`

	// macAddress is an optional MAC address to assign to the interface.
	// +optional
	MACAddress string `json:"macAddress,omitempty"`
}

// ManagedArtifacts tracks the libvirt artifacts created and managed by the controller.
type ManagedArtifacts struct {
	// domainName is the name of the libvirt domain.
	// +optional
	DomainName string `json:"domainName,omitempty"`

	// rootDiskVolume is the name of the root disk volume in the storage pool.
	// +optional
	RootDiskVolume string `json:"rootDiskVolume,omitempty"`

	// bootstrapISO is the name of the bootstrap ISO volume in the storage pool.
	// Only used for cloud-init bootstrap format.
	// +optional
	BootstrapISO string `json:"bootstrapISO,omitempty"`

	// ignitionFile is the path to the ignition JSON file on the host.
	// Only used for ignition bootstrap format (delivered via fw_cfg).
	// +optional
	IgnitionFile string `json:"ignitionFile,omitempty"`

	// nvramPath is the path to the NVRAM file on the host. Empty on
	// session-mode hosts, where libvirt places the NVRAM file in the
	// per-user daemon's own nvram directory.
	// +optional
	NVRAMPath string `json:"nvramPath,omitempty"`

	// ephemeralPoolName is the name of the tmpfs pool created by CAPLV.
	// Only set when spec.rootDisk.ephemeralPool is true.
	// +optional
	EphemeralPoolName string `json:"ephemeralPoolName,omitempty"`

	// additionalDiskVolumes is a list of additional disk volume names.
	// +optional
	AdditionalDiskVolumes []string `json:"additionalDiskVolumes,omitempty"`
}

// LibvirtMachineSpec defines the desired state of LibvirtMachine.
type LibvirtMachineSpec struct {
	// hostRef is a reference to a LibvirtHost resource in the same namespace.
	// +required
	HostRef corev1.LocalObjectReference `json:"hostRef"`

	// domain defines the virtual machine domain configuration.
	// +required
	Domain DomainSpec `json:"domain"`

	// rootDisk defines the root disk configuration.
	// +required
	RootDisk RootDiskSpec `json:"rootDisk"`

	// additionalDisks is an optional list of additional data disks to attach.
	// +optional
	AdditionalDisks []AdditionalDiskSpec `json:"additionalDisks,omitempty"`

	// network defines the network configuration for the virtual machine.
	// +required
	Network NetworkSpec `json:"network"`

	// bootstrapFormat specifies the format of bootstrap data to generate.
	// +required
	// +kubebuilder:default="ignition"
	BootstrapFormat BootstrapFormat `json:"bootstrapFormat"`

	// providerID is the identifier used by CAPI to reference this machine.
	// Set by the controller after the domain is created.
	// +optional
	ProviderID *string `json:"providerID,omitempty"`

	// nodeLabels are labels to apply to the Kubernetes Node once kubelet
	// registers it with the cluster. Unlike CAPI's Machine.spec.nodeLabels
	// (which enforces a prefix allow-list to avoid bypassing NodeRestriction),
	// CAPLV patches the Node directly from the controller's own identity, so
	// arbitrary keys (e.g. "dynatrace", "aqua", "k8s.ovn.org/egress-assignable")
	// are accepted. Keys removed from this map on a later update are removed
	// from the Node; admin-applied labels that CAPLV never set are left
	// untouched.
	// +optional
	NodeLabels map[string]string `json:"nodeLabels,omitempty"`

	// nodeAnnotations are annotations to apply to the Kubernetes Node once
	// kubelet registers it with the cluster. Same ownership and removal
	// semantics as nodeLabels.
	// +optional
	NodeAnnotations map[string]string `json:"nodeAnnotations,omitempty"`
}

// LibvirtMachineStatus defines the observed state of LibvirtMachine.
type LibvirtMachineStatus struct {
	// ready indicates whether the machine infrastructure is ready.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// initialization reports one-time provisioning milestones, as defined by
	// the Cluster API v1beta2 InfraMachine contract. Cluster API releases that
	// use the v1beta2 contract read initialization.provisioned instead of
	// ready; both are set.
	// +optional
	Initialization *LibvirtMachineInitializationStatus `json:"initialization,omitempty"`

	// failureDomain is the failure domain the machine runs in, as defined by
	// the Cluster API v1beta2 InfraMachine contract. Failure domains are
	// LibvirtHosts, so this is always spec.hostRef.name.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	FailureDomain string `json:"failureDomain,omitempty"`

	// addresses is the list of addresses assigned to the machine.
	// +optional
	Addresses []clusterv1.MachineAddress `json:"addresses,omitempty"`

	// domainUUID is the UUID of the libvirt domain.
	// +optional
	DomainUUID string `json:"domainUUID,omitempty"`

	// domainState is the current state of the libvirt domain (e.g. running, shutoff).
	// +optional
	DomainState string `json:"domainState,omitempty"`

	// managedArtifacts tracks the libvirt artifacts managed by the controller.
	// +optional
	ManagedArtifacts *ManagedArtifacts `json:"managedArtifacts,omitempty"`

	// failureReason is a machine-readable string indicating the reason for a failure.
	// +optional
	FailureReason *string `json:"failureReason,omitempty"`

	// failureMessage is a human-readable description of the failure.
	// +optional
	FailureMessage *string `json:"failureMessage,omitempty"`

	// conditions represent the current state of the LibvirtMachine resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// LibvirtMachineInitializationStatus reports one-time provisioning milestones
// of a LibvirtMachine.
// +kubebuilder:validation:MinProperties=1
type LibvirtMachineInitializationStatus struct {
	// provisioned is true once the domain has been created and started.
	// Once true it is never set back to false.
	// +optional
	Provisioned *bool `json:"provisioned,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:resource:categories=cluster-api,shortName=libvirtm
// +kubebuilder:printcolumn:name="Host",type="string",JSONPath=".spec.hostRef.name"
// +kubebuilder:printcolumn:name="Ready",type="boolean",JSONPath=".status.ready"
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.domainState"
// +kubebuilder:printcolumn:name="ProviderID",type="string",JSONPath=".spec.providerID",priority=1
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// LibvirtMachine is the Schema for the libvirtmachines API.
// It represents a virtual machine managed by libvirt as a Cluster API infrastructure machine.
type LibvirtMachine struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of LibvirtMachine.
	// +required
	Spec LibvirtMachineSpec `json:"spec"`

	// status defines the observed state of LibvirtMachine.
	// +optional
	Status LibvirtMachineStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// LibvirtMachineList contains a list of LibvirtMachine.
type LibvirtMachineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []LibvirtMachine `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LibvirtMachine{}, &LibvirtMachineList{})
}
