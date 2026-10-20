# API Reference

## Packages
- [infrastructure.cluster.x-k8s.io/v1alpha1](#infrastructureclusterx-k8siov1alpha1)


## infrastructure.cluster.x-k8s.io/v1alpha1

Package v1alpha1 contains API Schema definitions for the infrastructure v1alpha1 API group.

### Resource Types
- [LibvirtCluster](#libvirtcluster)
- [LibvirtClusterIdentity](#libvirtclusteridentity)
- [LibvirtClusterTemplate](#libvirtclustertemplate)
- [LibvirtHost](#libvirthost)
- [LibvirtMachine](#libvirtmachine)
- [LibvirtMachineTemplate](#libvirtmachinetemplate)



#### APIEndpoint



APIEndpoint represents a reachable Kubernetes API endpoint.



_Appears in:_
- [LibvirtClusterSpec](#libvirtclusterspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `host` _string_ | host is the hostname on which the API server is serving. |  |  |
| `port` _integer_ | port is the port on which the API server is serving. |  |  |


#### AdditionalDiskSpec



AdditionalDiskSpec defines an additional data disk.



_Appears in:_
- [LibvirtMachineSpec](#libvirtmachinespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | name is a unique identifier for this disk within the machine. |  | MinLength: 1 <br />Pattern: `^[A-Za-z0-9_.:-]+$` <br />Required: \{\} <br /> |
| `size` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#quantity-resource-api)_ | size is the desired size of the disk. |  | Required: \{\} <br /> |
| `storagePool` _string_ | storagePool is the name of the libvirt storage pool to use. |  | MinLength: 1 <br />Pattern: `^[A-Za-z0-9_.:-]+$` <br />Required: \{\} <br /> |
| `bus` _string_ | bus is the disk bus type. | virtio | Optional: \{\} <br /> |


#### AllowedNamespaces



AllowedNamespaces selects namespaces by name or by label.



_Appears in:_
- [LibvirtClusterIdentitySpec](#libvirtclusteridentityspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `list` _string array_ | list names the allowed namespaces. |  | MaxItems: 256 <br />items:MaxLength: 63 <br />items:MinLength: 1 <br />Optional: \{\} <br /> |
| `selector` _[LabelSelector](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#labelselector-v1-meta)_ | selector selects allowed namespaces by their labels. An empty selector<br />(\{\}) matches every namespace. |  | Optional: \{\} <br /> |


#### BaseImageSource



BaseImageSource selects one transport-specific source for the qcow2.



_Appears in:_
- [BaseImageSpec](#baseimagespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _[BootArtifactsSourceType](#bootartifactssourcetype)_ |  |  | Enum: [HTTPS OCI S3] <br />Required: \{\} <br /> |
| `https` _[HTTPSBaseImageSource](#httpsbaseimagesource)_ |  |  | Optional: \{\} <br /> |
| `oci` _[OCIBaseImageSource](#ocibaseimagesource)_ |  |  | Optional: \{\} <br /> |
| `s3` _[S3BaseImageSource](#s3baseimagesource)_ |  |  | Optional: \{\} <br /> |


#### BaseImageSpec



BaseImageSpec describes a qcow2 base image to stage in each libvirt host's
storage pool. Once staged, LibvirtMachine.spec.rootDisk.baseImage refers to
it by the volume name configured here.



_Appears in:_
- [LibvirtClusterSpec](#libvirtclusterspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `pool` _string_ | Pool is the libvirt storage pool name on each host where the image is<br />staged. Must exist on every LibvirtHost in the cluster. |  | MinLength: 1 <br />Pattern: `^[A-Za-z0-9_.:-]+$` <br />Required: \{\} <br /> |
| `volumeName` _string_ | VolumeName is the libvirt volume name the staged image will register as<br />inside Pool. Use the same value in LibvirtMachine.spec.rootDisk.baseImage. |  | MinLength: 1 <br />Pattern: `^[A-Za-z0-9_.:-]+$` <br />Required: \{\} <br /> |
| `source` _[BaseImageSource](#baseimagesource)_ | Source describes where to fetch the qcow2 from. Gzip-wrapped payloads<br />(URL ending in .gz, OCI mediaType application/gzip, or any blob whose<br />first two bytes are 0x1f 0x8b) are decompressed transparently before<br />the digest is computed. |  | Required: \{\} <br /> |


#### BootArtifactsSecretReference



BootArtifactsSecretReference points at a Secret holding pull credentials.



_Appears in:_
- [HTTPSBaseImageSource](#httpsbaseimagesource)
- [OCIBaseImageSource](#ocibaseimagesource)
- [OCIBootArtifactsSource](#ocibootartifactssource)
- [S3BaseImageSource](#s3baseimagesource)
- [S3BootArtifactsSource](#s3bootartifactssource)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name of the Secret. |  | MinLength: 1 <br />Required: \{\} <br /> |
| `namespace` _string_ | Namespace of the Secret. Deprecated: the Secret must live in the<br />LibvirtCluster's own namespace, so the only accepted values are "" and<br />that namespace. Naming any other namespace is rejected — the controller<br />reads Secrets with cluster-wide RBAC and this spec also chooses the<br />endpoint the credential is sent to, so an unconfined reference here is a<br />credential-exfiltration primitive. |  | Optional: \{\} <br /> |


#### BootArtifactsSource



BootArtifactsSource selects one transport-specific source.



_Appears in:_
- [BootArtifactsSpec](#bootartifactsspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _[BootArtifactsSourceType](#bootartifactssourcetype)_ |  |  | Enum: [HTTPS OCI S3] <br />Required: \{\} <br /> |
| `https` _[HTTPSBootArtifactsSource](#httpsbootartifactssource)_ |  |  | Optional: \{\} <br /> |
| `oci` _[OCIBootArtifactsSource](#ocibootartifactssource)_ |  |  | Optional: \{\} <br /> |
| `s3` _[S3BootArtifactsSource](#s3bootartifactssource)_ |  |  | Optional: \{\} <br /> |


#### BootArtifactsSourceType

_Underlying type:_ _string_

BootArtifactsSourceType enumerates supported artifact transports.



_Appears in:_
- [BaseImageSource](#baseimagesource)
- [BootArtifactsSource](#bootartifactssource)

| Field | Description |
| --- | --- |
| `HTTPS` |  |
| `OCI` |  |
| `S3` |  |


#### BootArtifactsSpec



BootArtifactsSpec configures direct-kernel-boot of first-boot artifacts.



_Appears in:_
- [LibvirtClusterSpec](#libvirtclusterspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `hostPath` _string_ | HostPath is the directory on each libvirt host where kernel/initramfs are cached.<br />Files land at <HostPath>/<sha256>/vmlinuz and <HostPath>/<sha256>/initramfs.img. |  | Pattern: `^/[A-Za-z0-9._/-]+$` <br />Required: \{\} <br /> |
| `kernelArgs` _string_ | KernelArgs is the cmdline appended to the direct-boot kernel. The user is<br />responsible for setting an ignition.config.url that reads from the virtio-blk<br />ignition disk (serial=ignition), e.g.<br />  "ignition.platform.id=metal ignition.config.url=oem:/dev/disk/by-id/virtio-ignition console=ttyS0" |  | MinLength: 1 <br />Required: \{\} <br /> |
| `source` _[BootArtifactsSource](#bootartifactssource)_ | Source describes where to fetch the kernel and initramfs from. |  | Required: \{\} <br /> |


#### BootstrapFormat

_Underlying type:_ _string_

BootstrapFormat defines the format of bootstrap data.

_Validation:_
- Enum: [ignition cloud-init]

_Appears in:_
- [LibvirtMachineSpec](#libvirtmachinespec)

| Field | Description |
| --- | --- |
| `ignition` |  |
| `cloud-init` |  |


#### CloneStrategy

_Underlying type:_ _string_

CloneStrategy defines how the root disk is cloned from the base image.

_Validation:_
- Enum: [copy-on-write full-clone]

_Appears in:_
- [RootDiskSpec](#rootdiskspec)

| Field | Description |
| --- | --- |
| `copy-on-write` |  |
| `full-clone` |  |


#### DNSSpec



DNSSpec defines DNS configuration for the network.



_Appears in:_
- [NetworkSpec](#networkspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `nameservers` _string array_ | nameservers is a list of DNS server addresses. |  | Optional: \{\} <br /> |
| `searchDomains` _string array_ | searchDomains is a list of DNS search domains. |  | Optional: \{\} <br /> |


#### DomainSpec



DomainSpec defines the virtual machine domain configuration.



_Appears in:_
- [LibvirtMachineSpec](#libvirtmachinespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `vcpus` _integer_ | vcpus is the number of virtual CPUs to allocate.<br />If zero or omitted, auto-sized from the LibvirtHost's available<br />capacity (total vCPUs minus reserved). |  | Minimum: 0 <br />Optional: \{\} <br /> |
| `memoryMB` _integer_ | memoryMB is the amount of memory in megabytes to allocate.<br />If zero or omitted, auto-sized from the LibvirtHost's available<br />capacity (total memory minus reserved). |  | Minimum: 0 <br />Optional: \{\} <br /> |
| `machine` _string_ | machine is the QEMU machine type. | q35 | Optional: \{\} <br /> |
| `firmware` _[Firmware](#firmware)_ | firmware is the firmware type for the virtual machine. | uefi | Enum: [uefi bios] <br />Optional: \{\} <br /> |


#### FailureDomain



FailureDomain describes one failure domain, matching the Cluster API v1beta2
FailureDomain type.



_Appears in:_
- [LibvirtClusterStatus](#libvirtclusterstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | name is the name of the failure domain: the name of a LibvirtHost. |  | MaxLength: 256 <br />MinLength: 1 <br />Required: \{\} <br /> |
| `controlPlane` _boolean_ | controlPlane determines if this failure domain is suitable for use by<br />control plane machines. |  | Optional: \{\} <br /> |
| `attributes` _object (keys:string, values:string)_ | attributes is a free form map of attributes an infrastructure provider<br />might use or require. |  | Optional: \{\} <br /> |


#### Firmware

_Underlying type:_ _string_

Firmware defines the firmware type for the virtual machine.

_Validation:_
- Enum: [uefi bios]

_Appears in:_
- [DomainSpec](#domainspec)

| Field | Description |
| --- | --- |
| `uefi` |  |
| `bios` |  |


#### HTTPSBaseImageSource



HTTPSBaseImageSource fetches the qcow2 over HTTPS.



_Appears in:_
- [BaseImageSource](#baseimagesource)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `url` _string_ | URL is the https:// (or http:// when explicitly allowed by the<br />caller's network policy) URL to the qcow2 file. |  | Required: \{\} <br /> |
| `sha256` _string_ | SHA256 of the decompressed qcow2. Optional but strongly recommended<br />when the URL is mutable; pinned digests prevent accidental version<br />drift across the fleet. |  | Optional: \{\} <br /> |
| `credentialsSecretRef` _[BootArtifactsSecretReference](#bootartifactssecretreference)_ | CredentialsSecretRef is a Secret with `username`/`password` keys for<br />HTTP Basic auth, or the kubernetes.io/dockerconfigjson layout. |  | Optional: \{\} <br /> |
| `insecureSkipTLSVerify` _boolean_ | InsecureSkipTLSVerify disables TLS certificate verification when<br />fetching the qcow2. Intended only for development or self-signed<br />endpoints; prefer adding the serving CA to the controller's trust<br />store (e.g. via SSL_CERT_FILE) for production mirrors. |  | Optional: \{\} <br /> |


#### HTTPSBootArtifactsSource



HTTPSBootArtifactsSource fetches kernel+initramfs over HTTPS.



_Appears in:_
- [BootArtifactsSource](#bootartifactssource)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `kernelURL` _string_ |  |  | Required: \{\} <br /> |
| `initramfsURL` _string_ |  |  | Required: \{\} <br /> |
| `kernelSHA256` _string_ |  |  | Optional: \{\} <br /> |
| `initramfsSHA256` _string_ |  |  | Optional: \{\} <br /> |


#### HostCapacity



HostCapacity reports the discovered and available resources on a libvirt host.



_Appears in:_
- [LibvirtHostStatus](#libvirthoststatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `totalVCPUs` _integer_ | totalVCPUs is the total number of CPU threads on the host. |  | Optional: \{\} <br /> |
| `totalMemoryMB` _integer_ | totalMemoryMB is the total physical memory on the host in MB. |  | Optional: \{\} <br /> |
| `availableVCPUs` _integer_ | availableVCPUs is the vCPUs available for CAPLV after reservations. |  | Optional: \{\} <br /> |
| `availableMemoryMB` _integer_ | availableMemoryMB is the memory available for CAPLV after reservations in MB. |  | Optional: \{\} <br /> |


#### LibvirtCluster



LibvirtCluster is the Schema for the libvirtclusters API.
It fulfills the Cluster API infrastructure cluster contract.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `infrastructure.cluster.x-k8s.io/v1alpha1` | | |
| `kind` _string_ | `LibvirtCluster` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  | Optional: \{\} <br /> |
| `spec` _[LibvirtClusterSpec](#libvirtclusterspec)_ | spec defines the desired state of LibvirtCluster. |  | Required: \{\} <br /> |
| `status` _[LibvirtClusterStatus](#libvirtclusterstatus)_ | status defines the observed state of LibvirtCluster. |  | Optional: \{\} <br /> |


#### LibvirtClusterIdentity



LibvirtClusterIdentity is a cluster-scoped SSH credential that LibvirtHosts
in the namespaces it allows can reference with spec.identityRef instead of
carrying their own spec.secretRef. Its Secret lives in the controller's
namespace, so the authors of those hosts use the credential without being
able to read it.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `infrastructure.cluster.x-k8s.io/v1alpha1` | | |
| `kind` _string_ | `LibvirtClusterIdentity` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  | Optional: \{\} <br /> |
| `spec` _[LibvirtClusterIdentitySpec](#libvirtclusteridentityspec)_ | spec defines the desired state of LibvirtClusterIdentity |  | Required: \{\} <br /> |


#### LibvirtClusterIdentityReference



LibvirtClusterIdentityReference names a LibvirtClusterIdentity.



_Appears in:_
- [LibvirtHostSpec](#libvirthostspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | name is the name of the LibvirtClusterIdentity. |  | MaxLength: 253 <br />MinLength: 1 <br />Required: \{\} <br /> |


#### LibvirtClusterIdentitySpec



LibvirtClusterIdentitySpec defines the SSH credential an identity holds and
the namespaces whose LibvirtHosts may use it.



_Appears in:_
- [LibvirtClusterIdentity](#libvirtclusteridentity)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `secretName` _string_ | secretName is the name of a Secret in the controller's namespace holding<br />the SSH private key under the `ssh-privatekey` key, the same shape as a<br />LibvirtHost's spec.secretRef Secret. The controller reads it on every<br />connection, so replacing the key in the Secret, or pointing secretName<br />at a new Secret, rotates the credential for every host using this<br />identity without editing the hosts. |  | MaxLength: 253 <br />MinLength: 1 <br />Required: \{\} <br /> |
| `allowedNamespaces` _[AllowedNamespaces](#allowednamespaces)_ | allowedNamespaces selects the namespaces whose LibvirtHosts may use<br />this identity. A namespace is allowed if it is in list or matches<br />selector. An empty allowedNamespaces (\{\}) allows every namespace.<br />When unset, no namespace may use the identity. |  | Optional: \{\} <br /> |


#### LibvirtClusterInitializationStatus



LibvirtClusterInitializationStatus reports one-time provisioning milestones
of a LibvirtCluster.

_Validation:_
- MinProperties: 1

_Appears in:_
- [LibvirtClusterStatus](#libvirtclusterstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `provisioned` _boolean_ | provisioned is true once the cluster infrastructure is ready for the<br />control plane. Once true it is never set back to false. |  | Optional: \{\} <br /> |


#### LibvirtClusterSpec



LibvirtClusterSpec defines the desired state of LibvirtCluster.



_Appears in:_
- [LibvirtCluster](#libvirtcluster)
- [LibvirtClusterTemplateResource](#libvirtclustertemplateresource)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `controlPlaneEndpoint` _[APIEndpoint](#apiendpoint)_ | controlPlaneEndpoint represents the endpoint used to communicate with the<br />control plane.<br />Optional, because an unset endpoint is a legitimate state rather than an<br />invalid object: the cluster simply reports Ready=false with reason<br />WaitingForControlPlaneEndpoint until a host and a valid port appear. It<br />also has to be omittable for LibvirtClusterTemplate, where baking one<br />endpoint into a template would stamp the same address onto every cluster<br />created from it. |  | Optional: \{\} <br /> |
| `bootArtifacts` _[BootArtifactsSpec](#bootartifactsspec)_ | bootArtifacts, when set, switches first-boot ignition delivery from<br />QEMU fw_cfg to libvirt direct-kernel-boot plus a virtio-blk ignition<br />disk. The kernel's qemu_fw_cfg driver does O(n²) offset reads, which<br />burns seconds of wall-clock time for multi-MB ignition payloads. |  | Optional: \{\} <br /> |
| `baseImage` _[BaseImageSpec](#baseimagespec)_ | baseImage, when set, lets the controller distribute the cluster's<br />root-disk qcow2 base image to every libvirt host that runs a machine in<br />this cluster. The image is fetched once into the controller-local cache<br />and SCP'd onto each host as needed; subsequent machines on the same<br />host reuse the already-staged volume. Hosts no longer need the qcow2<br />pre-staged via Ansible. |  | Optional: \{\} <br /> |


#### LibvirtClusterStatus



LibvirtClusterStatus defines the observed state of LibvirtCluster.



_Appears in:_
- [LibvirtCluster](#libvirtcluster)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `ready` _boolean_ | ready indicates whether the cluster infrastructure is ready. |  | Optional: \{\} <br /> |
| `initialization` _[LibvirtClusterInitializationStatus](#libvirtclusterinitializationstatus)_ | initialization reports one-time provisioning milestones, as defined by<br />the Cluster API v1beta2 InfraCluster contract. Cluster API releases that<br />use the v1beta2 contract read initialization.provisioned instead of<br />ready; both are set. |  | MinProperties: 1 <br />Optional: \{\} <br /> |
| `failureDomains` _[FailureDomain](#failuredomain) array_ | failureDomains lists the failure domains available to machines of this<br />cluster, in the list shape of the Cluster API v1beta2 InfraCluster<br />contract. Each LibvirtHost in the cluster's namespace is its own failure<br />domain, named after the host: a host is an independent hypervisor whose<br />loss takes down only the VMs it runs. |  | MaxItems: 10000 <br />Optional: \{\} <br /> |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#condition-v1-meta) array_ | conditions represent the current state of the LibvirtCluster resource. |  | Optional: \{\} <br /> |


#### LibvirtClusterTemplate



LibvirtClusterTemplate is the Schema for the libvirtclustertemplates API.

A ClusterClass references this through spec.infrastructure.ref and stamps a
LibvirtCluster from it per cluster in the topology. The type exists so
ClusterClass can name a CAPLV infrastructure template at all.

Templates are inert: nothing reconciles them, so there is no status.

spec.template.spec.controlPlaneEndpoint should normally be left unset here.
Each cluster has its own endpoint, and a value baked into the template is
copied onto every cluster created from it. A stamped cluster with no
endpoint reports Ready=false with reason WaitingForControlPlaneEndpoint
until one is set, which is the intended path; the validating webhook warns
when a template carries one.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `infrastructure.cluster.x-k8s.io/v1alpha1` | | |
| `kind` _string_ | `LibvirtClusterTemplate` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  | Optional: \{\} <br /> |
| `spec` _[LibvirtClusterTemplateSpec](#libvirtclustertemplatespec)_ | spec defines the desired state of LibvirtClusterTemplate. |  | Required: \{\} <br /> |


#### LibvirtClusterTemplateResource



LibvirtClusterTemplateResource is the per-cluster payload a ClusterClass
stamps out.



_Appears in:_
- [LibvirtClusterTemplateSpec](#libvirtclustertemplatespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `metadata` _[ObjectMeta](#objectmeta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  | Optional: \{\} <br /> |
| `spec` _[LibvirtClusterSpec](#libvirtclusterspec)_ | spec is the LibvirtCluster spec stamped onto each created cluster. |  | Required: \{\} <br /> |


#### LibvirtClusterTemplateSpec



LibvirtClusterTemplateSpec defines the desired state of
LibvirtClusterTemplate.



_Appears in:_
- [LibvirtClusterTemplate](#libvirtclustertemplate)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `template` _[LibvirtClusterTemplateResource](#libvirtclustertemplateresource)_ | template is the cluster template. |  | Required: \{\} <br /> |


#### LibvirtHost



LibvirtHost is the Schema for the libvirthosts API.
It represents a reusable libvirt host connection configuration.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `infrastructure.cluster.x-k8s.io/v1alpha1` | | |
| `kind` _string_ | `LibvirtHost` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  | Optional: \{\} <br /> |
| `spec` _[LibvirtHostSpec](#libvirthostspec)_ | spec defines the desired state of LibvirtHost. |  | Required: \{\} <br /> |
| `status` _[LibvirtHostStatus](#libvirthoststatus)_ | status defines the observed state of LibvirtHost. |  | Optional: \{\} <br /> |


#### LibvirtHostSpec



LibvirtHostSpec defines the desired state of LibvirtHost.



_Appears in:_
- [LibvirtHost](#libvirthost)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `uri` _string_ | uri is the libvirt connection URI (e.g. qemu+ssh://user@host/system).<br />The path selects the libvirt daemon on the host: /system (default)<br />connects to the privileged system daemon; /session connects to the<br />per-user daemon of the SSH user. Session mode requires host setup for<br />unprivileged networking (qemu-bridge-helper) and only supports<br />network.type=bridge on machines. |  | MinLength: 1 <br />Required: \{\} <br /> |
| `secretRef` _[SecretReference](#secretreference)_ | secretRef is a reference to a Secret in the host's namespace containing<br />the SSH private key for authenticating to the libvirt host, under the<br />`ssh-privatekey` key. Exactly one of secretRef and identityRef must be<br />set. |  | Optional: \{\} <br /> |
| `identityRef` _[LibvirtClusterIdentityReference](#libvirtclusteridentityreference)_ | identityRef names a cluster-scoped LibvirtClusterIdentity holding the<br />SSH private key, for credentials shared across hosts and namespaces<br />and rotated in one place. The identity must allow this host's<br />namespace. Exactly one of secretRef and identityRef must be set. |  | Optional: \{\} <br /> |
| `hostKeyFingerprint` _string_ | hostKeyFingerprint is the SSH host key fingerprint (SHA256:...) used to<br />verify the identity of the remote host. |  | Optional: \{\} <br /> |
| `firmwarePath` _string_ | firmwarePath overrides the OVMF firmware path on the host. | /usr/share/OVMF/OVMF_CODE.secboot.fd | Optional: \{\} <br /> |
| `nvramTemplatePath` _string_ | nvramTemplatePath is the UEFI VARS template on the host that each<br />machine's private NVRAM file is initialized from (the domain XML's<br /><nvram template=...>). It should be the VARS file matching the<br />firmwarePath loader build. | /usr/share/OVMF/OVMF_VARS.fd | Optional: \{\} <br /> |
| `reservedResources` _[ReservedResources](#reservedresources)_ | reservedResources specifies how much CPU and memory to reserve for the<br />host's incumbent workloads. CAPLV will not allocate VMs that exceed<br />the remaining capacity. If unset, defaults to reserving 2 vCPUs and<br />4096 MB of memory. |  | Optional: \{\} <br /> |
| `healthCheckIntervalSeconds` _integer_ | healthCheckIntervalSeconds is how often (in seconds) to recheck this<br />host when it has active machines. Hosts with no machines are not polled.<br />Default: 300 (5 minutes). | 300 | Minimum: 30 <br />Optional: \{\} <br /> |


#### LibvirtHostStatus



LibvirtHostStatus defines the observed state of LibvirtHost.



_Appears in:_
- [LibvirtHost](#libvirthost)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `ready` _boolean_ | ready indicates whether the host is reachable and usable. |  | Optional: \{\} <br /> |
| `capacity` _[HostCapacity](#hostcapacity)_ | capacity reports the discovered host resources and what is available<br />for CAPLV after reservations. |  | Optional: \{\} <br /> |
| `lastChecked` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#time-v1-meta)_ | lastChecked is the timestamp of the last connectivity check. |  | Optional: \{\} <br /> |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#condition-v1-meta) array_ | conditions represent the current state of the LibvirtHost resource. |  | Optional: \{\} <br /> |


#### LibvirtMachine



LibvirtMachine is the Schema for the libvirtmachines API.
It represents a virtual machine managed by libvirt as a Cluster API infrastructure machine.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `infrastructure.cluster.x-k8s.io/v1alpha1` | | |
| `kind` _string_ | `LibvirtMachine` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  | Optional: \{\} <br /> |
| `spec` _[LibvirtMachineSpec](#libvirtmachinespec)_ | spec defines the desired state of LibvirtMachine. |  | Required: \{\} <br /> |
| `status` _[LibvirtMachineStatus](#libvirtmachinestatus)_ | status defines the observed state of LibvirtMachine. |  | Optional: \{\} <br /> |


#### LibvirtMachineInitializationStatus



LibvirtMachineInitializationStatus reports one-time provisioning milestones
of a LibvirtMachine.

_Validation:_
- MinProperties: 1

_Appears in:_
- [LibvirtMachineStatus](#libvirtmachinestatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `provisioned` _boolean_ | provisioned is true once the domain has been created and started.<br />Once true it is never set back to false. |  | Optional: \{\} <br /> |


#### LibvirtMachineSpec



LibvirtMachineSpec defines the desired state of LibvirtMachine.



_Appears in:_
- [LibvirtMachine](#libvirtmachine)
- [LibvirtMachineTemplateResource](#libvirtmachinetemplateresource)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `hostRef` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#localobjectreference-v1-core)_ | hostRef is a reference to a LibvirtHost resource in the same namespace. |  | Required: \{\} <br /> |
| `domain` _[DomainSpec](#domainspec)_ | domain defines the virtual machine domain configuration. |  | Required: \{\} <br /> |
| `rootDisk` _[RootDiskSpec](#rootdiskspec)_ | rootDisk defines the root disk configuration. |  | Required: \{\} <br /> |
| `additionalDisks` _[AdditionalDiskSpec](#additionaldiskspec) array_ | additionalDisks is an optional list of additional data disks to attach. |  | Optional: \{\} <br /> |
| `network` _[NetworkSpec](#networkspec)_ | network defines the network configuration for the virtual machine. |  | Required: \{\} <br /> |
| `bootstrapFormat` _[BootstrapFormat](#bootstrapformat)_ | bootstrapFormat specifies the format of bootstrap data to generate. | ignition | Enum: [ignition cloud-init] <br />Required: \{\} <br /> |
| `providerID` _string_ | providerID is the identifier used by CAPI to reference this machine.<br />Set by the controller after the domain is created. |  | Optional: \{\} <br /> |
| `nodeLabels` _object (keys:string, values:string)_ | nodeLabels are labels to apply to the Kubernetes Node once kubelet<br />registers it with the cluster. Unlike CAPI's Machine.spec.nodeLabels<br />(which enforces a prefix allow-list to avoid bypassing NodeRestriction),<br />CAPLV patches the Node directly from the controller's own identity, so<br />arbitrary keys (e.g. "dynatrace", "aqua", "k8s.ovn.org/egress-assignable")<br />are accepted. Keys removed from this map on a later update are removed<br />from the Node; admin-applied labels that CAPLV never set are left<br />untouched. |  | Optional: \{\} <br /> |
| `nodeAnnotations` _object (keys:string, values:string)_ | nodeAnnotations are annotations to apply to the Kubernetes Node once<br />kubelet registers it with the cluster. Same ownership and removal<br />semantics as nodeLabels. |  | Optional: \{\} <br /> |


#### LibvirtMachineStatus



LibvirtMachineStatus defines the observed state of LibvirtMachine.



_Appears in:_
- [LibvirtMachine](#libvirtmachine)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `ready` _boolean_ | ready indicates whether the machine infrastructure is ready. |  | Optional: \{\} <br /> |
| `initialization` _[LibvirtMachineInitializationStatus](#libvirtmachineinitializationstatus)_ | initialization reports one-time provisioning milestones, as defined by<br />the Cluster API v1beta2 InfraMachine contract. Cluster API releases that<br />use the v1beta2 contract read initialization.provisioned instead of<br />ready; both are set. |  | MinProperties: 1 <br />Optional: \{\} <br /> |
| `failureDomain` _string_ | failureDomain is the failure domain the machine runs in, as defined by<br />the Cluster API v1beta2 InfraMachine contract. Failure domains are<br />LibvirtHosts, so this is always spec.hostRef.name. |  | MaxLength: 256 <br />MinLength: 1 <br />Optional: \{\} <br /> |
| `addresses` _[MachineAddress](#machineaddress) array_ | addresses is the list of addresses assigned to the machine. |  | Optional: \{\} <br /> |
| `domainUUID` _string_ | domainUUID is the UUID of the libvirt domain. |  | Optional: \{\} <br /> |
| `domainState` _string_ | domainState is the current state of the libvirt domain (e.g. running, shutoff). |  | Optional: \{\} <br /> |
| `managedArtifacts` _[ManagedArtifacts](#managedartifacts)_ | managedArtifacts tracks the libvirt artifacts managed by the controller. |  | Optional: \{\} <br /> |
| `failureReason` _string_ | failureReason is a machine-readable string indicating the reason for a failure. |  | Optional: \{\} <br /> |
| `failureMessage` _string_ | failureMessage is a human-readable description of the failure. |  | Optional: \{\} <br /> |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#condition-v1-meta) array_ | conditions represent the current state of the LibvirtMachine resource. |  | Optional: \{\} <br /> |


#### LibvirtMachineTemplate



LibvirtMachineTemplate is the Schema for the libvirtmachinetemplates API.

MachineDeployment and KubeadmControlPlane reference a template through
spec.infrastructureRef and stamp a LibvirtMachine per replica from it. The
type exists so those higher-order resources can be used at all.

One caveat, because CAPLV's machine model is not replica-shaped: a
LibvirtMachine names its host through the required spec.hostRef and declares
a static network identity, and a host runs exactly one VM. Every machine
stamped from one template therefore carries the same host and the same
address, so a replica count above one produces machines that collide rather
than scale. Scaling needs per-machine host and address allocation, which
CAPLV does not do yet. The validating webhook warns about this on create.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `infrastructure.cluster.x-k8s.io/v1alpha1` | | |
| `kind` _string_ | `LibvirtMachineTemplate` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  | Optional: \{\} <br /> |
| `spec` _[LibvirtMachineTemplateSpec](#libvirtmachinetemplatespec)_ | spec defines the desired state of LibvirtMachineTemplate. |  | Required: \{\} <br /> |
| `status` _[LibvirtMachineTemplateStatus](#libvirtmachinetemplatestatus)_ | status defines the observed state of LibvirtMachineTemplate. |  | Optional: \{\} <br /> |


#### LibvirtMachineTemplateResource



LibvirtMachineTemplateResource is the per-machine payload a MachineDeployment
or KubeadmControlPlane stamps out.



_Appears in:_
- [LibvirtMachineTemplateSpec](#libvirtmachinetemplatespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `metadata` _[ObjectMeta](#objectmeta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  | Optional: \{\} <br /> |
| `spec` _[LibvirtMachineSpec](#libvirtmachinespec)_ | spec is the LibvirtMachine spec stamped onto each created machine. |  | Required: \{\} <br /> |


#### LibvirtMachineTemplateSpec



LibvirtMachineTemplateSpec defines the desired state of LibvirtMachineTemplate.



_Appears in:_
- [LibvirtMachineTemplate](#libvirtmachinetemplate)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `template` _[LibvirtMachineTemplateResource](#libvirtmachinetemplateresource)_ | template is the machine template. |  | Required: \{\} <br /> |


#### LibvirtMachineTemplateStatus



LibvirtMachineTemplateStatus defines the observed state of
LibvirtMachineTemplate.

Templates are inert -- nothing reconciles them -- so this carries only the
capacity field the CAPI contract reads for cluster autoscaling.



_Appears in:_
- [LibvirtMachineTemplate](#libvirtmachinetemplate)



#### MachineAddress



MachineAddress contains information for the node's address.



_Appears in:_
- [LibvirtMachineStatus](#libvirtmachinestatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _[MachineAddressType](#machineaddresstype)_ | type is the machine address type, one of Hostname, ExternalIP, InternalIP, ExternalDNS or InternalDNS. |  |  |
| `address` _string_ | address is the machine address. |  |  |


#### MachineAddressType

_Underlying type:_ _string_

MachineAddressType is the type of a MachineAddress: one of Hostname,
ExternalIP, InternalIP, ExternalDNS or InternalDNS.



_Appears in:_
- [MachineAddress](#machineaddress)

| Field | Description |
| --- | --- |
| `InternalIP` |  |


#### ManagedArtifacts



ManagedArtifacts tracks the libvirt artifacts created and managed by the controller.



_Appears in:_
- [LibvirtMachineStatus](#libvirtmachinestatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `domainName` _string_ | domainName is the name of the libvirt domain. |  | Optional: \{\} <br /> |
| `rootDiskVolume` _string_ | rootDiskVolume is the name of the root disk volume in the storage pool. |  | Optional: \{\} <br /> |
| `bootstrapISO` _string_ | bootstrapISO is the name of the bootstrap ISO volume in the storage pool.<br />Only used for cloud-init bootstrap format. |  | Optional: \{\} <br /> |
| `ignitionFile` _string_ | ignitionFile is the path to the ignition JSON file on the host.<br />Only used for ignition bootstrap format (delivered via fw_cfg). |  | Optional: \{\} <br /> |
| `nvramPath` _string_ | nvramPath is the path to the NVRAM file on the host. Empty on<br />session-mode hosts, where libvirt places the NVRAM file in the<br />per-user daemon's own nvram directory. |  | Optional: \{\} <br /> |
| `ephemeralPoolName` _string_ | ephemeralPoolName is the name of the tmpfs pool created by CAPLV.<br />Only set when spec.rootDisk.ephemeralPool is true. |  | Optional: \{\} <br /> |
| `additionalDiskVolumes` _string array_ | additionalDiskVolumes is a list of additional disk volume names. |  | Optional: \{\} <br /> |


#### NetworkSpec



NetworkSpec defines the network configuration for the virtual machine.



_Appears in:_
- [LibvirtMachineSpec](#libvirtmachinespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _[NetworkType](#networktype)_ | type is the type of libvirt network attachment. |  | Enum: [bridge network] <br />Required: \{\} <br /> |
| `name` _string_ | name is the name of the libvirt network or bridge device. |  | MinLength: 1 <br />Required: \{\} <br /> |
| `model` _string_ | model is the NIC model. | virtio | Optional: \{\} <br /> |
| `addresses` _string array_ | addresses is a list of static IP addresses in CIDR notation (e.g. 192.168.1.10/24). |  | MinItems: 1 <br />Required: \{\} <br /> |
| `gateway` _string_ | gateway is the default gateway address. |  | Optional: \{\} <br /> |
| `dns` _[DNSSpec](#dnsspec)_ | dns defines DNS configuration for the network. |  | Optional: \{\} <br /> |
| `macAddress` _string_ | macAddress is an optional MAC address to assign to the interface. When<br />unset, CAPLV derives a stable one from the domain name in QEMU's<br />52:54:00 range, which cloud-init guests use to match the NIC to the<br />static address configuration CAPLV gives them. |  | Optional: \{\} <br /> |


#### NetworkType

_Underlying type:_ _string_

NetworkType defines the type of libvirt network attachment.

_Validation:_
- Enum: [bridge network]

_Appears in:_
- [NetworkSpec](#networkspec)

| Field | Description |
| --- | --- |
| `bridge` |  |
| `network` |  |


#### OCIBaseImageSource



OCIBaseImageSource fetches the qcow2 from a single-blob OCI artifact.
The manifest is expected to contain one layer carrying the qcow2; if the
layer is annotated with `org.opencontainers.image.title`, BlobTitle
selects it, otherwise the single layer is taken implicitly.



_Appears in:_
- [BaseImageSource](#baseimagesource)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `reference` _string_ | Reference is the full OCI reference, e.g. "ghcr.io/example/rhcos:4.18". |  | Required: \{\} <br /> |
| `blobTitle` _string_ | BlobTitle is the `org.opencontainers.image.title` annotation<br />identifying the qcow2 layer. When empty and the manifest has exactly<br />one layer, that layer is selected automatically. |  | Optional: \{\} <br /> |
| `plainHTTP` _boolean_ | PlainHTTP allows non-TLS pulls from the registry. |  | Optional: \{\} <br /> |
| `insecureSkipTLSVerify` _boolean_ | InsecureSkipTLSVerify disables TLS certificate verification. |  | Optional: \{\} <br /> |
| `credentialsSecretRef` _[BootArtifactsSecretReference](#bootartifactssecretreference)_ | CredentialsSecretRef references a Secret holding registry credentials. |  | Optional: \{\} <br /> |
| `sha256` _string_ | SHA256 of the decompressed qcow2. Optional but recommended. |  | Optional: \{\} <br /> |


#### OCIBootArtifactsSource



OCIBootArtifactsSource fetches kernel+initramfs from a single OCI artifact.
The artifact is expected to be an `oras push`-style manifest whose layers
each carry an `org.opencontainers.image.title` annotation matching the file
name. KernelLayerTitle and InitramfsLayerTitle select the two layers; the
resolver fetches only those two blobs.



_Appears in:_
- [BootArtifactsSource](#bootartifactssource)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `reference` _string_ | Reference is the full OCI reference, e.g. "ghcr.io/example/boot:v1". |  | Required: \{\} <br /> |
| `kernelLayerTitle` _string_ | KernelLayerTitle is the org.opencontainers.image.title annotation that<br />identifies the kernel layer in the manifest. Defaults to "vmlinuz". |  | Optional: \{\} <br /> |
| `initramfsLayerTitle` _string_ | InitramfsLayerTitle is the org.opencontainers.image.title annotation<br />that identifies the initramfs layer. Defaults to "initramfs.img". |  | Optional: \{\} <br /> |
| `plainHTTP` _boolean_ | PlainHTTP allows non-TLS pulls from the registry. Intended only for<br />in-cluster mirrors and development. Defaults to false. |  | Optional: \{\} <br /> |
| `insecureSkipTLSVerify` _boolean_ | InsecureSkipTLSVerify disables TLS certificate verification on the<br />registry endpoint. Intended only for self-signed dev registries. |  | Optional: \{\} <br /> |
| `credentialsSecretRef` _[BootArtifactsSecretReference](#bootartifactssecretreference)_ | CredentialsSecretRef references a Secret in the LibvirtCluster's<br />namespace containing registry credentials.<br />The Secret is read in two formats, in order of preference:<br />  1. `.dockerconfigjson` (a kubernetes.io/dockerconfigjson Secret).<br />  2. plain `username` / `password` keys. |  | Optional: \{\} <br /> |
| `kernelSHA256` _string_ |  |  | Optional: \{\} <br /> |
| `initramfsSHA256` _string_ |  |  | Optional: \{\} <br /> |


#### ObjectMeta



ObjectMeta is the subset of metav1.ObjectMeta a template propagates to the
objects created from it: labels and annotations.



_Appears in:_
- [LibvirtClusterTemplateResource](#libvirtclustertemplateresource)
- [LibvirtMachineTemplateResource](#libvirtmachinetemplateresource)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `labels` _object (keys:string, values:string)_ | labels is a map of string keys and values that can be used to organize and categorize<br />(scope and select) objects. May match selectors of replication controllers<br />and services.<br />More info: http://kubernetes.io/docs/user-guide/labels |  | Optional: \{\} <br /> |
| `annotations` _object (keys:string, values:string)_ | annotations is an unstructured key value map stored with a resource that may be<br />set by external tools to store and retrieve arbitrary metadata. They are not<br />queryable and should be preserved when modifying objects.<br />More info: http://kubernetes.io/docs/user-guide/annotations |  | Optional: \{\} <br /> |


#### ReservedResources



ReservedResources defines resources reserved for the host's incumbent workloads.



_Appears in:_
- [LibvirtHostSpec](#libvirthostspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `vcpus` _integer_ | vcpus is the number of vCPUs to reserve for the host. Default: 2. | 2 | Minimum: 0 <br />Optional: \{\} <br /> |
| `memoryMB` _integer_ | memoryMB is the amount of memory in MB to reserve for the host. Default: 4096. | 4096 | Minimum: 0 <br />Optional: \{\} <br /> |


#### RootDiskSpec



RootDiskSpec defines the root disk configuration.



_Appears in:_
- [LibvirtMachineSpec](#libvirtmachinespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `size` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#quantity-resource-api)_ | size is the desired size of the root disk. |  | Required: \{\} <br /> |
| `storagePool` _string_ | storagePool is the name of the pre-existing libvirt storage pool<br />that receives the root disk volume (and the NoCloud ISO for<br />cloud-init guests). Not used as the disk target when ephemeralPool<br />is true — CAPLV then provisions its own per-machine tmpfs pool<br />instead — though baseImagePool still defaults to this value when<br />unset. |  | MinLength: 1 <br />Pattern: `^[A-Za-z0-9_.:-]+$` <br />Required: \{\} <br /> |
| `baseImage` _string_ | baseImage is the name of the base image volume to clone from. |  | MinLength: 1 <br />Pattern: `^[A-Za-z0-9_.:-]+$` <br />Required: \{\} <br /> |
| `baseImagePool` _string_ | baseImagePool is the storage pool containing the base image.<br />If unset, defaults to storagePool. This allows the base image to<br />live on persistent storage while ephemeral VM disks use a different<br />pool (e.g., tmpfs-backed for ephemeral workers). |  | Pattern: `^[A-Za-z0-9_.:-]+$` <br />Optional: \{\} <br /> |
| `ephemeralPool` _boolean_ | ephemeralPool, when true, causes CAPLV to create a per-machine<br />tmpfs-backed pool named <namespace>-<cluster>-<machine>-pool<br />(surfaced in status.artifacts.ephemeralPoolName) at provisioning<br />time and destroy it, including the tmpfs mount, at deletion time.<br />The root disk and any bootstrap ISO are placed in this generated<br />pool instead of storagePool. This ensures the host's persistent<br />storage is never touched and RAM is only consumed while the VM<br />exists. Set baseImagePool to the persistent pool holding the base<br />image. |  | Optional: \{\} <br /> |
| `ephemeralPoolSize` _string_ | ephemeralPoolSize caps the RAM the ephemeral tmpfs mount may consume.<br />Accepts tmpfs `size=` syntax: a percentage of physical RAM (e.g. "80%")<br />or an absolute size (e.g. "16G"). When unset, the kernel default (50%<br />of physical RAM) applies. Only meaningful when ephemeralPool is true. |  | Pattern: `^([1-9][0-9]?\|100)%$\|^[1-9][0-9]*[KMGTkmgt]?$` <br />Optional: \{\} <br /> |
| `bus` _string_ | bus is the disk bus type. | virtio | Optional: \{\} <br /> |
| `cloneStrategy` _[CloneStrategy](#clonestrategy)_ | cloneStrategy defines how the root disk is cloned from the base image. | copy-on-write | Enum: [copy-on-write full-clone] <br />Optional: \{\} <br /> |


#### S3BaseImageSource



S3BaseImageSource fetches the qcow2 from an S3-compatible object store.



_Appears in:_
- [BaseImageSource](#baseimagesource)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `endpoint` _string_ | Endpoint is the S3 endpoint host[:port]. |  | Required: \{\} <br /> |
| `region` _string_ | Region for AWS S3; optional for MinIO/Ceph. |  | Optional: \{\} <br /> |
| `bucket` _string_ |  |  | Required: \{\} <br /> |
| `key` _string_ |  |  | Required: \{\} <br /> |
| `usePathStyle` _boolean_ | UsePathStyle forces path-style addressing. |  | Optional: \{\} <br /> |
| `insecure` _boolean_ | Insecure switches the endpoint to plain HTTP. |  | Optional: \{\} <br /> |
| `insecureSkipTLSVerify` _boolean_ | InsecureSkipTLSVerify disables TLS certificate verification. |  | Optional: \{\} <br /> |
| `credentialsSecretRef` _[BootArtifactsSecretReference](#bootartifactssecretreference)_ | CredentialsSecretRef references a Secret holding static credentials. |  | Optional: \{\} <br /> |
| `sha256` _string_ | SHA256 of the decompressed qcow2. Optional but recommended. |  | Optional: \{\} <br /> |


#### S3BootArtifactsSource



S3BootArtifactsSource fetches kernel+initramfs from an S3-compatible store.



_Appears in:_
- [BootArtifactsSource](#bootartifactssource)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `endpoint` _string_ | Endpoint is the S3 endpoint host[:port], e.g. "s3.amazonaws.com" or<br />"minio.svc.cluster.local:9000". |  | Required: \{\} <br /> |
| `region` _string_ | Region is the bucket's region. Optional for MinIO/Ceph; required by<br />AWS S3 (defaults to "us-east-1" if empty). |  | Optional: \{\} <br /> |
| `bucket` _string_ |  |  | Required: \{\} <br /> |
| `kernelKey` _string_ |  |  | Required: \{\} <br /> |
| `initramfsKey` _string_ |  |  | Required: \{\} <br /> |
| `usePathStyle` _boolean_ | UsePathStyle forces path-style addressing (bucket as URL path) rather<br />than virtual-hosted style. Required by MinIO and some Ceph setups. |  | Optional: \{\} <br /> |
| `insecure` _boolean_ | Insecure switches the endpoint to plain HTTP. Defaults to false. |  | Optional: \{\} <br /> |
| `insecureSkipTLSVerify` _boolean_ | InsecureSkipTLSVerify disables TLS certificate verification. |  | Optional: \{\} <br /> |
| `credentialsSecretRef` _[BootArtifactsSecretReference](#bootartifactssecretreference)_ | CredentialsSecretRef references a Secret in the LibvirtCluster's<br />namespace containing static credentials.<br />Recognized keys: `accessKeyID`, `secretAccessKey`, optional<br />`sessionToken`. If unset, the resolver attempts anonymous reads. |  | Optional: \{\} <br /> |
| `kernelSHA256` _string_ |  |  | Optional: \{\} <br /> |
| `initramfsSHA256` _string_ |  |  | Optional: \{\} <br /> |


#### SecretReference



SecretReference is a reference to a Kubernetes Secret.



_Appears in:_
- [LibvirtHostSpec](#libvirthostspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | name is the name of the Secret. |  | MinLength: 1 <br />Required: \{\} <br /> |
| `namespace` _string_ | namespace is the namespace of the Secret. Deprecated: the Secret must<br />live in the same namespace as the resource that references it, so the<br />only accepted values are "" and that namespace. Naming any other<br />namespace is rejected — the controller reads Secrets with cluster-wide<br />RBAC, so honouring an arbitrary namespace here would let the author of a<br />namespaced resource borrow a Secret they cannot read themselves. |  | Optional: \{\} <br /> |


