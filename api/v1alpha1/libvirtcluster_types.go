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

// LibvirtClusterSpec defines the desired state of LibvirtCluster.
type LibvirtClusterSpec struct {
	// controlPlaneEndpoint represents the endpoint used to communicate with the
	// control plane.
	//
	// Optional, because an unset endpoint is a legitimate state rather than an
	// invalid object: the cluster simply reports Ready=false with reason
	// WaitingForControlPlaneEndpoint until a host and a valid port appear. It
	// also has to be omittable for LibvirtClusterTemplate, where baking one
	// endpoint into a template would stamp the same address onto every cluster
	// created from it.
	// +optional
	ControlPlaneEndpoint clusterv1.APIEndpoint `json:"controlPlaneEndpoint,omitempty"`

	// bootArtifacts, when set, switches first-boot ignition delivery from
	// QEMU fw_cfg to libvirt direct-kernel-boot plus a virtio-blk ignition
	// disk. The kernel's qemu_fw_cfg driver does O(n²) offset reads, which
	// burns seconds of wall-clock time for multi-MB ignition payloads.
	// +optional
	BootArtifacts *BootArtifactsSpec `json:"bootArtifacts,omitempty"`

	// baseImage, when set, lets the controller distribute the cluster's
	// root-disk qcow2 base image to every libvirt host that runs a machine in
	// this cluster. The image is fetched once into the controller-local cache
	// and SCP'd onto each host as needed; subsequent machines on the same
	// host reuse the already-staged volume. Hosts no longer need the qcow2
	// pre-staged via Ansible.
	// +optional
	BaseImage *BaseImageSpec `json:"baseImage,omitempty"`
}

// BaseImageSpec describes a qcow2 base image to stage in each libvirt host's
// storage pool. Once staged, LibvirtMachine.spec.rootDisk.baseImage refers to
// it by the volume name configured here.
type BaseImageSpec struct {
	// Pool is the libvirt storage pool name on each host where the image is
	// staged. Must exist on every LibvirtHost in the cluster.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.:-]+$`
	Pool string `json:"pool"`

	// VolumeName is the libvirt volume name the staged image will register as
	// inside Pool. Use the same value in LibvirtMachine.spec.rootDisk.baseImage.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.:-]+$`
	VolumeName string `json:"volumeName"`

	// Source describes where to fetch the qcow2 from. Gzip-wrapped payloads
	// (URL ending in .gz, OCI mediaType application/gzip, or any blob whose
	// first two bytes are 0x1f 0x8b) are decompressed transparently before
	// the digest is computed.
	// +kubebuilder:validation:Required
	Source BaseImageSource `json:"source"`
}

// BaseImageSource selects one transport-specific source for the qcow2.
type BaseImageSource struct {
	// +kubebuilder:validation:Enum=HTTPS;OCI;S3
	// +kubebuilder:validation:Required
	Type BootArtifactsSourceType `json:"type"`

	// +optional
	HTTPS *HTTPSBaseImageSource `json:"https,omitempty"`
	// +optional
	OCI *OCIBaseImageSource `json:"oci,omitempty"`
	// +optional
	S3 *S3BaseImageSource `json:"s3,omitempty"`
}

// HTTPSBaseImageSource fetches the qcow2 over HTTPS.
type HTTPSBaseImageSource struct {
	// URL is the https:// (or http:// when explicitly allowed by the
	// caller's network policy) URL to the qcow2 file.
	// +kubebuilder:validation:Required
	URL string `json:"url"`
	// SHA256 of the decompressed qcow2. Optional but strongly recommended
	// when the URL is mutable; pinned digests prevent accidental version
	// drift across the fleet.
	// +optional
	SHA256 string `json:"sha256,omitempty"`
	// CredentialsSecretRef is a Secret with `username`/`password` keys for
	// HTTP Basic auth, or the kubernetes.io/dockerconfigjson layout.
	// +optional
	CredentialsSecretRef *BootArtifactsSecretReference `json:"credentialsSecretRef,omitempty"`

	// InsecureSkipTLSVerify disables TLS certificate verification when
	// fetching the qcow2. Intended only for development or self-signed
	// endpoints; prefer adding the serving CA to the controller's trust
	// store (e.g. via SSL_CERT_FILE) for production mirrors.
	// +optional
	InsecureSkipTLSVerify bool `json:"insecureSkipTLSVerify,omitempty"`
}

// OCIBaseImageSource fetches the qcow2 from a single-blob OCI artifact.
// The manifest is expected to contain one layer carrying the qcow2; if the
// layer is annotated with `org.opencontainers.image.title`, BlobTitle
// selects it, otherwise the single layer is taken implicitly.
type OCIBaseImageSource struct {
	// Reference is the full OCI reference, e.g. "ghcr.io/example/rhcos:4.18".
	// +kubebuilder:validation:Required
	Reference string `json:"reference"`

	// BlobTitle is the `org.opencontainers.image.title` annotation
	// identifying the qcow2 layer. When empty and the manifest has exactly
	// one layer, that layer is selected automatically.
	// +optional
	BlobTitle string `json:"blobTitle,omitempty"`

	// PlainHTTP allows non-TLS pulls from the registry.
	// +optional
	PlainHTTP bool `json:"plainHTTP,omitempty"`

	// InsecureSkipTLSVerify disables TLS certificate verification.
	// +optional
	InsecureSkipTLSVerify bool `json:"insecureSkipTLSVerify,omitempty"`

	// CredentialsSecretRef references a Secret holding registry credentials.
	// +optional
	CredentialsSecretRef *BootArtifactsSecretReference `json:"credentialsSecretRef,omitempty"`

	// SHA256 of the decompressed qcow2. Optional but recommended.
	// +optional
	SHA256 string `json:"sha256,omitempty"`
}

// S3BaseImageSource fetches the qcow2 from an S3-compatible object store.
type S3BaseImageSource struct {
	// Endpoint is the S3 endpoint host[:port].
	// +kubebuilder:validation:Required
	Endpoint string `json:"endpoint"`
	// Region for AWS S3; optional for MinIO/Ceph.
	// +optional
	Region string `json:"region,omitempty"`
	// +kubebuilder:validation:Required
	Bucket string `json:"bucket"`
	// +kubebuilder:validation:Required
	Key string `json:"key"`

	// UsePathStyle forces path-style addressing.
	// +optional
	UsePathStyle bool `json:"usePathStyle,omitempty"`
	// Insecure switches the endpoint to plain HTTP.
	// +optional
	Insecure bool `json:"insecure,omitempty"`
	// InsecureSkipTLSVerify disables TLS certificate verification.
	// +optional
	InsecureSkipTLSVerify bool `json:"insecureSkipTLSVerify,omitempty"`

	// CredentialsSecretRef references a Secret holding static credentials.
	// +optional
	CredentialsSecretRef *BootArtifactsSecretReference `json:"credentialsSecretRef,omitempty"`

	// SHA256 of the decompressed qcow2. Optional but recommended.
	// +optional
	SHA256 string `json:"sha256,omitempty"`
}

// BootArtifactsSpec configures direct-kernel-boot of first-boot artifacts.
type BootArtifactsSpec struct {
	// HostPath is the directory on each libvirt host where kernel/initramfs are cached.
	// Files land at <HostPath>/<sha256>/vmlinuz and <HostPath>/<sha256>/initramfs.img.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^/[A-Za-z0-9._/-]+$`
	HostPath string `json:"hostPath"`

	// KernelArgs is the cmdline appended to the direct-boot kernel. The user is
	// responsible for setting an ignition.config.url that reads from the virtio-blk
	// ignition disk (serial=ignition), e.g.
	//   "ignition.platform.id=metal ignition.config.url=oem:/dev/disk/by-id/virtio-ignition console=ttyS0"
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	KernelArgs string `json:"kernelArgs"`

	// Source describes where to fetch the kernel and initramfs from.
	// +kubebuilder:validation:Required
	Source BootArtifactsSource `json:"source"`
}

// BootArtifactsSourceType enumerates supported artifact transports.
type BootArtifactsSourceType string

const (
	BootArtifactsSourceHTTPS BootArtifactsSourceType = "HTTPS"
	BootArtifactsSourceOCI   BootArtifactsSourceType = "OCI"
	BootArtifactsSourceS3    BootArtifactsSourceType = "S3"
)

// BootArtifactsSource selects one transport-specific source.
type BootArtifactsSource struct {
	// +kubebuilder:validation:Enum=HTTPS;OCI;S3
	// +kubebuilder:validation:Required
	Type BootArtifactsSourceType `json:"type"`

	// +optional
	HTTPS *HTTPSBootArtifactsSource `json:"https,omitempty"`
	// +optional
	OCI *OCIBootArtifactsSource `json:"oci,omitempty"`
	// +optional
	S3 *S3BootArtifactsSource `json:"s3,omitempty"`
}

// HTTPSBootArtifactsSource fetches kernel+initramfs over HTTPS.
type HTTPSBootArtifactsSource struct {
	// +kubebuilder:validation:Required
	KernelURL string `json:"kernelURL"`
	// +kubebuilder:validation:Required
	InitramfsURL string `json:"initramfsURL"`
	// +optional
	KernelSHA256 string `json:"kernelSHA256,omitempty"`
	// +optional
	InitramfsSHA256 string `json:"initramfsSHA256,omitempty"`
}

// OCIBootArtifactsSource fetches kernel+initramfs from a single OCI artifact.
// The artifact is expected to be an `oras push`-style manifest whose layers
// each carry an `org.opencontainers.image.title` annotation matching the file
// name. KernelLayerTitle and InitramfsLayerTitle select the two layers; the
// resolver fetches only those two blobs.
type OCIBootArtifactsSource struct {
	// Reference is the full OCI reference, e.g. "ghcr.io/example/boot:v1".
	// +kubebuilder:validation:Required
	Reference string `json:"reference"`

	// KernelLayerTitle is the org.opencontainers.image.title annotation that
	// identifies the kernel layer in the manifest. Defaults to "vmlinuz".
	// +optional
	KernelLayerTitle string `json:"kernelLayerTitle,omitempty"`

	// InitramfsLayerTitle is the org.opencontainers.image.title annotation
	// that identifies the initramfs layer. Defaults to "initramfs.img".
	// +optional
	InitramfsLayerTitle string `json:"initramfsLayerTitle,omitempty"`

	// PlainHTTP allows non-TLS pulls from the registry. Intended only for
	// in-cluster mirrors and development. Defaults to false.
	// +optional
	PlainHTTP bool `json:"plainHTTP,omitempty"`

	// InsecureSkipTLSVerify disables TLS certificate verification on the
	// registry endpoint. Intended only for self-signed dev registries.
	// +optional
	InsecureSkipTLSVerify bool `json:"insecureSkipTLSVerify,omitempty"`

	// CredentialsSecretRef references a Secret in the LibvirtCluster's
	// namespace containing registry credentials.
	// The Secret is read in two formats, in order of preference:
	//   1. `.dockerconfigjson` (a kubernetes.io/dockerconfigjson Secret).
	//   2. plain `username` / `password` keys.
	// +optional
	CredentialsSecretRef *BootArtifactsSecretReference `json:"credentialsSecretRef,omitempty"`

	// +optional
	KernelSHA256 string `json:"kernelSHA256,omitempty"`
	// +optional
	InitramfsSHA256 string `json:"initramfsSHA256,omitempty"`
}

// S3BootArtifactsSource fetches kernel+initramfs from an S3-compatible store.
type S3BootArtifactsSource struct {
	// Endpoint is the S3 endpoint host[:port], e.g. "s3.amazonaws.com" or
	// "minio.svc.cluster.local:9000".
	// +kubebuilder:validation:Required
	Endpoint string `json:"endpoint"`

	// Region is the bucket's region. Optional for MinIO/Ceph; required by
	// AWS S3 (defaults to "us-east-1" if empty).
	// +optional
	Region string `json:"region,omitempty"`

	// +kubebuilder:validation:Required
	Bucket string `json:"bucket"`
	// +kubebuilder:validation:Required
	KernelKey string `json:"kernelKey"`
	// +kubebuilder:validation:Required
	InitramfsKey string `json:"initramfsKey"`

	// UsePathStyle forces path-style addressing (bucket as URL path) rather
	// than virtual-hosted style. Required by MinIO and some Ceph setups.
	// +optional
	UsePathStyle bool `json:"usePathStyle,omitempty"`

	// Insecure switches the endpoint to plain HTTP. Defaults to false.
	// +optional
	Insecure bool `json:"insecure,omitempty"`

	// InsecureSkipTLSVerify disables TLS certificate verification.
	// +optional
	InsecureSkipTLSVerify bool `json:"insecureSkipTLSVerify,omitempty"`

	// CredentialsSecretRef references a Secret in the LibvirtCluster's
	// namespace containing static credentials.
	// Recognized keys: `accessKeyID`, `secretAccessKey`, optional
	// `sessionToken`. If unset, the resolver attempts anonymous reads.
	// +optional
	CredentialsSecretRef *BootArtifactsSecretReference `json:"credentialsSecretRef,omitempty"`

	// +optional
	KernelSHA256 string `json:"kernelSHA256,omitempty"`
	// +optional
	InitramfsSHA256 string `json:"initramfsSHA256,omitempty"`
}

// BootArtifactsSecretReference points at a Secret holding pull credentials.
type BootArtifactsSecretReference struct {
	// Name of the Secret.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// Namespace of the Secret. Deprecated: the Secret must live in the
	// LibvirtCluster's own namespace, so the only accepted values are "" and
	// that namespace. Naming any other namespace is rejected — the controller
	// reads Secrets with cluster-wide RBAC and this spec also chooses the
	// endpoint the credential is sent to, so an unconfined reference here is a
	// credential-exfiltration primitive.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// LibvirtClusterStatus defines the observed state of LibvirtCluster.
type LibvirtClusterStatus struct {
	// ready indicates whether the cluster infrastructure is ready.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// conditions represent the current state of the LibvirtCluster resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:resource:categories=cluster-api,shortName=libvirtcl
// +kubebuilder:printcolumn:name="Ready",type="boolean",JSONPath=".status.ready"
// +kubebuilder:printcolumn:name="Endpoint",type="string",JSONPath=".spec.controlPlaneEndpoint.host"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// LibvirtCluster is the Schema for the libvirtclusters API.
// It fulfills the Cluster API infrastructure cluster contract.
type LibvirtCluster struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of LibvirtCluster.
	// +required
	Spec LibvirtClusterSpec `json:"spec"`

	// status defines the observed state of LibvirtCluster.
	// +optional
	Status LibvirtClusterStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// LibvirtClusterList contains a list of LibvirtCluster.
type LibvirtClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []LibvirtCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LibvirtCluster{}, &LibvirtClusterList{})
}
