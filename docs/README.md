# CAPLV documentation

Start with [Getting started](getting-started.md). The project overview and
compatibility table are in the [repository README](../README.md).

| Guide | Contents |
| --- | --- |
| [Getting started](getting-started.md) | Prerequisites, one-time cluster and host setup, creating a first worker directly or through 5-Spot |
| [Session mode](session-mode.md) | Running VMs under an unprivileged per-user libvirt daemon |
| [Configuration](configuration.md) | Ephemeral tmpfs storage, node labels, direct kernel boot, cluster-wide base images, private CAs, shared SSH credentials, `clusterctl move`, watch filters, feature gates, failure domains |
| [Cluster templates](cluster-templates.md) | `clusterctl generate cluster` flavors and their variables |
| [Architecture](architecture.md) | Design decisions, host security model, storage pools and host layout |
| [Troubleshooting](troubleshooting.md) | MachineHealthCheck setup, failure modes and recovery, known limitations |
| [Development](development.md) | Building, testing, code generation and project layout |
| [API reference](api-reference/v1alpha1.md) | Every field, default and validation rule, generated from `api/v1alpha1` |
| [Architecture decision records](adr/) | Rationale for host management, readiness, API version promotion, credential identities, `clusterctl move` and host and address allocation |
| [Demos](demos/) | Scripts and walkthroughs from the project demos |
