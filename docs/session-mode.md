# Session mode (unprivileged libvirt)

This page extends step 5 of [Getting started](getting-started.md#cluster-setup).

By default VMs are managed by the root-owned system libvirt daemon
(`/system` in the URI). Hosts that must not run a privileged VM manager
can instead use the service account's per-user daemon by ending the URI
with `/session`:

```yaml
spec:
  uri: "qemu+ssh://caplv@rhel-host-01.example.com/session"
```

In session mode QEMU runs as the service account itself. Because an
unprivileged process cannot create tap devices, bridge attachment goes
through QEMU's setuid `qemu-bridge-helper`, which only attaches to
bridges whitelisted in `/etc/qemu/bridge.conf`. The ansible playbook
configures all of this:

```bash
ansible-playbook -i <host>, -u root deploy/ansible/setup-host.yaml \
  -e libvirt_mode=session \
  -e '{"session_allowed_bridges": ["br0"]}'
```

which additionally: adds the service account to the `kvm` group (direct
`/dev/kvm` access), enables `loginctl` lingering (so VMs survive SSH
disconnect and the user daemon can autostart), makes `qemu-bridge-helper`
setuid, whitelists the bridges, and creates the storage pool under
`~caplv/.local/share/libvirt/images` in the user session.

The `LibvirtHost` health probe verifies session hosts end to end: on top
of the usual connectivity and KVM checks (which run against the session
daemon), it confirms `qemu-bridge-helper` is setuid (or has
`cap_net_admin`) and that lingering is enabled, marking the host
`Ready=false` with reason `SessionModeMisconfigured` otherwise.

On session hosts, UEFI machines get no explicit NVRAM path in their
domain XML: the system daemon's `/var/lib/libvirt/qemu/nvram/` is
root-owned and unwritable for the per-user daemon, so libvirt instead
allocates each machine's NVRAM file in its own directory
(`~caplv/.config/libvirt/qemu/nvram/`), initialized from the host's
`spec.nvramTemplatePath` and removed at undefine. Note this lives in the
service account's home — if that home is on NFS, see the caveat below.

Session mode limitations:

- Machines must use `network.type: bridge`. Libvirt-managed NAT networks
  (`network.type: network`) require the system daemon. The machine
  controller rejects this combination up front (terminal
  `NetworkTypeUnsupported` condition).
- `qemu-bridge-helper` does not support multi-queue virtio-net.
- On SELinux-enforcing hosts, sVirt isolation between guests is reduced
  (session guests run unconfined as the service account). If a guest
  image fails to open, check file contexts under the session pool path.
- The session daemon keeps its state — domain definitions, logs, and the
  per-machine UEFI NVRAM files QEMU holds open for write — under the
  service account's `~/.config` and `~/.cache`. If the account's home is
  on NFS, QEMU's image locking needs working byte-range locks (fine on
  NFSv4; NFSv3 needs a functioning lock manager) and an NFS outage can
  I/O-error running guests. Prefer a local home for the service account,
  or relocate the state by setting `XDG_CONFIG_HOME`/`XDG_CACHE_HOME` to
  a local path in the account's systemd user environment
  (`~/.config/environment.d/` is itself read from `$XDG_CONFIG_HOME`, so
  set it via `systemctl --user set-environment` under lingering, or a
  PAM env entry).
