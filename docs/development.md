# Development

```bash
# Build
make build

# Run tests
make test

# Generate CRDs and deepcopy
make manifests generate

# Regenerate the API reference (docs/api-reference/v1alpha1.md) after editing api/
make api-docs

# Build container image (pure Go, no CGo)
make podman-build IMG=ghcr.io/atgreen/caplv:latest

# Deploy to cluster
make deploy IMG=ghcr.io/atgreen/caplv:latest
```

## Project structure

```
api/v1alpha1/          CRD type definitions (LibvirtHost, LibvirtCluster, LibvirtMachine and the two templates)
feature/               Feature gates (--feature-gates)
internal/
  controller/          Reconcilers for the CRDs + CSR approver (50 concurrent machine workers)
  bootartifacts/       Kernel and initramfs fetching for direct kernel boot
  imagecache/          Base image fetching and caching (HTTPS, OCI, S3)
  ignition/            Ignition config manipulation (hostname and providerID injection)
  libvirt/             Client interface, virsh-over-SSH, domain XML generation
  iso/                 Cloud-init NoCloud ISO creation (pure Go, go-diskfs)
  ssh/                 SSH client with host key verification
  scope/               MachineScope — gathers reconciliation context, artifact naming
  webhook/             Admission webhooks (immutable spec, required static IP, one VM per host)
config/                Kubernetes manifests (CRDs, RBAC, deployment, webhook)
templates/             clusterctl cluster templates (flavors)
docs/                  User documentation, ADRs, generated API reference
```
