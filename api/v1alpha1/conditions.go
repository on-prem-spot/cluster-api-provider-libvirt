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

const (
	// LibvirtHost conditions
	HostReachableCondition = "Reachable"

	// LibvirtMachine conditions
	InfrastructureReadyCondition     = "InfrastructureReady"
	HostReachableForMachineCondition = "HostReachable"
	ArtifactsCreatedCondition        = "ArtifactsCreated"
	BootstrapDataReadyCondition      = "BootstrapDataReady"
	BaseImageStagedCondition         = "BaseImageStaged"
	CleanupStalledCondition          = "CleanupStalled"
	NodeLabelledCondition            = "NodeLabelled"

	// Reasons — terminal
	ReasonBaseImageNotFound     = "BaseImageNotFound"
	ReasonBaseImagePoolNotFound = "BaseImagePoolNotFound"
	ReasonStoragePoolNotFound   = "StoragePoolNotFound"
	ReasonHostUnauthorized      = "HostUnauthorized"
	ReasonDomainAlreadyExists   = "DomainAlreadyExists"
	ReasonInvalidBootstrapData  = "InvalidBootstrapData"
	ReasonSpecMismatch          = "SpecMismatch"
	// ReasonNetworkTypeUnsupported marks a machine whose network attachment
	// cannot work on its host — e.g. network.type=network on a session-mode
	// (qemu:///session) host, whose per-user daemon has no network driver.
	ReasonNetworkTypeUnsupported = "NetworkTypeUnsupported"

	// Reasons — transient
	ReasonStorageInsufficient   = "StorageInsufficient"
	ReasonConnectionFailed      = "ConnectionFailed"
	ReasonHypervisorUnavailable = "HypervisorUnavailable"
	// ReasonSessionModeMisconfigured marks a session-mode host missing setup
	// that unprivileged VMs depend on (setuid qemu-bridge-helper, user
	// lingering). Fixable on the host, so re-checked like other host probes.
	ReasonSessionModeMisconfigured = "SessionModeMisconfigured"

	// Reasons — waiting
	ReasonBootstrapDataNotReady = "BootstrapDataNotReady"
	ReasonHostNotReady          = "HostNotReady"
	ReasonNodeNotJoined         = "NodeNotJoined"
	ReasonBaseImageStaging      = "BaseImageStaging"
	// ReasonNodeOwnershipMismatch is set when the Node named by this machine's
	// artifact base name reports a different providerID, i.e. it was
	// provisioned by some other machine and must not be modified.
	ReasonNodeOwnershipMismatch = "NodeOwnershipMismatch"

	// Reasons — success
	ReasonConnectionSucceeded = "ConnectionSucceeded"
	ReasonProvisioned         = "Provisioned"
	ReasonArtifactsReady      = "ArtifactsReady"
	ReasonClusterReady        = "ClusterReady"
	ReasonNodeLabelled        = "NodeLabelled"
	ReasonBaseImageStaged     = "BaseImageStaged"
	ReasonBootstrapDataReady  = "BootstrapDataReady"

	// Reasons — cleanup
	ReasonCleanupStalled   = "CleanupStalled"
	ReasonCleanupSucceeded = "CleanupSucceeded"

	// Finalizers
	MachineFinalizer = "infrastructure.cluster.x-k8s.io/libvirt-machine"
	HostFinalizer    = "infrastructure.cluster.x-k8s.io/libvirt-host"

	// Node annotations CAPLV writes to track the label/annotation keys it
	// manages on a Node. Used to compute a clean removal when keys disappear
	// from spec.nodeLabels / spec.nodeAnnotations — admin-applied keys CAPLV
	// never set are left untouched.
	ManagedNodeLabelsAnnotation      = "infrastructure.cluster.x-k8s.io/libvirt-managed-labels"
	ManagedNodeAnnotationsAnnotation = "infrastructure.cluster.x-k8s.io/libvirt-managed-annotations"
)
