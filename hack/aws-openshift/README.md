# OpenShift test environment on AWS

These scripts test CAPLV the way it is meant to be used with OpenShift. CAPLV
runs on an OpenShift cluster and adds RHCOS worker VMs on libvirt hosts, and
those VMs join that same cluster. The cluster must be on platform `none`,
like an on-premises cluster. A cloud-platform cluster does not work, because
its worker configuration fetches the node name and provider ID from the
cloud's metadata service, and its cloud controller manager rejects nodes it
cannot find in the cloud.

The environment has three RHEL EC2 instances with nested virtualization.
One runs single-node OpenShift (SNO) as a VM. The other two are CAPLV's
libvirt hosts.

```
VPC 10.0.0.0/16, one public subnet
├── sno host   (m8i.4xlarge, RHEL 10) ── SNO VM 10.200.0.10 (agent-based install, platform none)
├── libvirt-1  (m8i.2xlarge, RHEL 10) ── CAPLV worker VMs on 10.200.1.0/24
└── libvirt-2  (m8i.2xlarge, RHEL 9)  ── CAPLV worker VMs on 10.200.2.0/24

DNS  private zone <cluster>.<base>: api, api-int, *.apps -> 10.200.0.10
     public records api, *.apps -> the SNO host, which forwards 6443/443/80 to the VM
```

The VPC only carries traffic for addresses that AWS assigned, so the VMs
are neither bridged nor NATed:

- Each host routes its own /24 with a libvirt network in `route` mode. The
  VPC route table sends that /24 to the host, and the host's
  source/destination check is off.
- The cluster and the VMs reach each other at their own addresses, which
  OpenShift needs for the kubelet and the OVN Geneve tunnels.
- The hosts masquerade only the VMs' internet-bound traffic, such as pulls
  from quay.io, since the internet gateway forwards only addresses AWS
  assigned.
- libvirt's dnsmasq on each host serves DNS to its VMs. It forwards to the
  VPC resolver, so the VMs resolve the cluster's private names.

The security group opens SSH, the API and the console only to the address
the scripts run from.

## Prerequisites

- An AWS profile (`AWS_PROFILE`, default `caplv`).
- An account with a public Route53 zone (`BASE_DOMAIN`). The scripts find it
  automatically when the account has only one.
- An OpenShift pull secret (`PULL_SECRET`, default `~/.openshift-pull-secret`).
- `aws`, `jq`, `ansible-playbook` and `ssh`. The scripts download
  `openshift-install`, `oc` and `clusterctl` into `~/.cache/caplv-aws/bin`.

All settings and their defaults are in [env.sh](env.sh). State, including
the kubeconfig and SSH keys, is kept in `~/.cache/caplv-aws/<cluster>`.

## Running

| Step | Script | Time | What it does |
| --- | --- | --- | --- |
| 1 | `./infra.sh` | ~10 min | Creates the VPC, the hosts and DNS. Prepares the hosts with `deploy/ansible/setup-host.yaml` and the routed networks. |
| 2 | `./sno.sh` | ~50 min | Installs SNO with the newest OpenShift as a VM on the SNO host, using the agent-based installer. `--render-only` writes the install and agent configs for review. |
| 3 | `./capi.sh` | ~5 min | Runs `clusterctl init --infrastructure libvirt` with the released CAPLV, plus the OpenShift SCC grant for upstream CAPI. |
| 4 | `./workers.sh` | ~20 min | Creates one RHCOS worker per libvirt host, following `docs/getting-started.md`. `--delete` removes them. Options: `BOOTSTRAP=pointer` uses OpenShift's worker pointer config instead of the full rendered one; `DIRECT_BOOT=1` boots with `bootArtifacts`; `HOST_KEY_TYPE=ecdsa` is needed with CAPLV v0.3.0. |
| 5 | `./down.sh` | ~5 min | Removes everything, then lists anything still tagged `caplv-e2e`. |

Every step can be run again. The environment costs about $2 an hour while
it exists, so run `./down.sh` when done.

```bash
export KUBECONFIG=~/.cache/caplv-aws/caplv-sno/install/auth/kubeconfig
# Console: https://console-openshift-console.apps.<cluster>.<base>, user kubeadmin,
# password in ~/.cache/caplv-aws/<cluster>/install/auth/kubeadmin-password
```
