# Demo: idle PostgreSQL standbys as OpenShift workers

A highly available PostgreSQL cluster keeps standby servers that mostly
wait. In this demo, CAPLV lends their spare capacity to OpenShift as worker
nodes that run batch jobs, and RHEL High Availability takes it back the
moment a standby is promoted.

```
            RHEL HA cluster (Pacemaker, fence_aws)
 ┌──────────────────┬──────────────────┬──────────────────┐
 │ libvirt-1        │ libvirt-2        │ libvirt-3        │
 │ PostgreSQL       │ PostgreSQL       │ PostgreSQL       │
 │ PRIMARY + VIP    │ standby (sync)   │ standby          │
 │ caplv-evict      │                  │                  │
 │ LibvirtHost      │ RHCOS worker VM  │ RHCOS worker VM  │
 │ cordoned         │ (OpenShift node) │ (OpenShift node) │
 └──────────────────┴──────────────────┴──────────────────┘
        ▲ embeddings written through the VIP     ▲
        └──────── Kueue Job on the borrowed workers ┘
```

## The pieces

- **RHEL High Availability**: three RHEL 10.2 servers built from the
  pay-as-you-go RHEL HA images.
  - Pacemaker runs PostgreSQL 16 with `ocf:heartbeat:pgsql`, using
    synchronous streaming replication.
  - It fences failed servers with `fence_aws`, which powers them off.
  - It moves a virtual IP (`awsvip` plus `IPaddr2`) to the primary.
- **CAPLV**: each server is a `LibvirtHost`. A two-replica
  MachineDeployment runs RHCOS worker VMs on the servers that are not
  primary. They join the single-node OpenShift cluster, labelled
  `caplv.demo/capacity=scavenged`.
- **The eviction agent** (`ocf:caplv:evict`, in
  `hack/aws-openshift/pgha/evict`): a Pacemaker resource colocated with the
  PostgreSQL primary, and ordered to start before its promotion. Starting it:
  1. hard-kills every VM on the server (`virsh destroy`);
  2. cordons the server's LibvirtHost (`spec.unschedulable`), so CAPLV
     neither restarts the VM nor places a new one there;
  3. deletes the dead worker's Machine in the background, so the
     MachineDeployment replaces it.

  Stopping it, when the primary moves away, uncordons the host. The database
  never waits for Kubernetes: the kill is local, and the API calls are
  bounded and run afterwards.
- **Kueue** (the Red Hat build): a ResourceFlavor for the scavenged nodes,
  and a queue for an Indexed Job that embeds the Kubernetes documentation
  with `BAAI/bge-base-en-v1.5`. The vectors are written to the same HA
  PostgreSQL, using pgvector from RHEL AppStream. Each shard skips chunks
  already stored, so the work resumes after a worker is killed.
- **The demo app** (`hack/aws-openshift/demo`): a guided narrative over
  live state from Pacemaker, CAPLV and CAPI, Kueue, PostgreSQL and EC2.
  - It measures the write outage with a probe that writes through the
    virtual IP twice a second.
  - It builds a timeline of what each layer did.
  - It can kill the primary, either by powering off its EC2 instance with
    no OS shutdown (`SkipOsShutdown`) or by crashing its kernel, and can
    re-seed it afterwards. A plain forced stop is not enough: EC2 still
    shuts the OS down first, and Pacemaker hands over in an orderly way.
  - It links to the matching OpenShift and AWS console pages.

## Running it

It runs on the AWS environment from `hack/aws-openshift` (see its README),
with the libvirt hosts built from the HA images:

```bash
cd hack/aws-openshift
./infra.sh                                              # if not done yet
./sno.sh && ./capi.sh                                   # OpenShift, CAPI, CAPLV
HOST_IMAGE_FAMILY=RHEL_HA HOST_RHEL_MAJORS="10 10 10" ./infra.sh
./pgha.sh                                               # Pacemaker, PostgreSQL, workers
./demo.sh                                               # Kueue, the app; prints its URL
```

The CAPLV build in the cluster must include `LibvirtHost.spec.unschedulable`.

## Walking through it

The app's chapters follow the story:
1. why the standbys are idle;
2. the database cluster;
3. how CAPLV lends the standbys to OpenShift;
4. starting the embedding run;
5. searching the vectors;
6. pulling the plug on the primary;
7. recovering it;
8. answers to the questions people usually ask.

To repeat the demo, recover the old primary (chapter 7), then reset the run
(chapter 4).

## Things to know

- **Expect about 27 seconds without writes** when the primary is powered
  off: about 15 seconds for corosync to declare it lost and fence it, then
  promotion and moving the virtual IP. The eviction agent has killed the
  worker VM and cordoned the host within a second of being started, well
  before the promotion. An orderly shutdown of the primary fails over in
  about 10 seconds, since nothing needs detecting or fencing.
- **A stopped and started instance gets a new public address.** The app
  works with the private ones, but the scripts reach the hosts through
  their public ones: run `./refresh-ips.sh` after a kill and recovery.

- **A failed primary does not rejoin by itself.** The PostgreSQL agent
  leaves a lock file on a node that was primary, and refuses to start there
  until the data is re-seeded. The app's recovery button runs
  `pg_basebackup` from the new primary, clears the lock and uncordons the
  host. Fencing powers nodes off rather than rebooting them for the same
  reason.
- **Changing the `pgsql` resource's parameters restarts it**, which also
  trips the lock on the primary. Clear `/var/lib/pgsql/tmp/PGSQL.lock`
  there only if that node was stopped cleanly as primary.
- **A stop failure gets a node fenced.** An agent misconfiguration (for
  example `awsvip` without `region`) can fence every node at once. Use
  `pcs property set maintenance-mode=true` while changing the configuration.
