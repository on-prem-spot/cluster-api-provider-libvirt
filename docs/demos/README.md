# Demo and local deployment artifacts

These files are a recorded walkthrough of one specific environment, not
supported interfaces of the provider. They reference concrete hosts,
addresses, and an OpenShift single-node install, and they are not exercised
by CI.

| Path | What it is |
| --- | --- |
| `DEMO.md`, `DEMO.html` | Walkthrough write-up. `DEMO.html` is published to GitHub Pages by `.github/workflows/pages.yaml`. |
| `deploy.sh` | Ad-hoc script to deploy the controller stack to a cluster. |
| `demo-setup-host.sh` | Creates the LibvirtHost, Cluster, and LibvirtCluster CRs. Run after `deploy.sh`. |
| `demo-create-worker.sh` | Creates a 5-Spot ScheduledMachine to add a worker. |
| `hosts.yaml` | Ansible inventory naming a specific hypervisor. |
| `sno-install/` | OpenShift single-node install and agent configs. |

Treat the host names, IP addresses, and SSH details in these files as
examples to replace, not defaults to adopt. For the supported install path,
see [Getting started](../getting-started.md) and `config/`.
