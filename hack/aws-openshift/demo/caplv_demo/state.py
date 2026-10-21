# Copyright 2026 Anthony Green.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Live state of the demo, gathered from every layer, and the timeline of
what changed. One collector thread refreshes it; the web app only reads it.

Sources:
  Pacemaker   crm_mon over SSH to the RHEL HA hosts (any one that answers)
  CAPLV/CAPI  LibvirtHosts, Machines, the MachineDeployment, Nodes
  Kueue       Workloads and the embedding Job's pods
  PostgreSQL  through the virtual IP: who is primary, replication, rows
  AWS         EC2 instance states
"""

import io
import json
import os
import threading
import time
import xml.etree.ElementTree as ET
from datetime import datetime
from dataclasses import dataclass, field

import boto3
import paramiko
import psycopg
from kubernetes import client, config

from caplv_demo import db

NS_CAPLV = os.environ.get("CAPLV_NAMESPACE", "caplv-workers")
NS_DEMO = os.environ.get("DEMO_NAMESPACE", "caplv-demo")
MD_NAME = os.environ.get("MACHINE_DEPLOYMENT", "pg-ha-workers")
JOB_NAME = os.environ.get("JOB_NAME", "embed")
# Where the kill and the timeline are kept, so a restarted app still knows
# which host it killed and what happened since.
STATE_CONFIGMAP = os.environ.get("STATE_CONFIGMAP", "caplv-demo-state")
REGION = os.environ.get("AWS_REGION", "us-east-2")
TAG = os.environ.get("AWS_TAG", "caplv-e2e=caplv-sno")
# name=private-ip pairs of the RHEL HA hosts, e.g. "libvirt-1=10.0.1.5,..."
HOSTS = dict(pair.split("=", 1) for pair in os.environ.get("PG_HOSTS", "").split(",") if pair)
SSH_KEY = os.environ.get("SSH_KEY_PATH", "/etc/caplv-demo/ssh/id_ed25519")
SSH_USER = os.environ.get("SSH_USER", "ec2-user")


def now() -> float:
    return time.time()


@dataclass
class Timeline:
    """What happened, when. Most events are added when the collector sees a
    change, so their times are when the change was observed; the eviction
    agent's log lines and fencing carry the time they happened."""

    events: list = field(default_factory=list)
    lock: threading.Lock = field(default_factory=threading.Lock)

    def add(self, kind: str, text: str, layer: str, at: float | None = None, unique: bool = False) -> None:
        """Append an event. With unique, an event of the same kind and text at
        the same time is not added twice (sources read again after a restart)."""
        with self.lock:
            at = at or now()
            if unique and any(e["kind"] == kind and e["text"] == text and abs(e["at"] - at) < 0.01
                              for e in self.events):
                return
            self.events.append({"at": at, "kind": kind, "text": text, "layer": layer})
            # Events stamped with a source's own time can arrive late.
            self.events.sort(key=lambda e: e["at"])
            del self.events[:-300]

    def since(self, t: float) -> list:
        with self.lock:
            return [e for e in self.events if e["at"] >= t]


class Probe:
    """Writes a heartbeat row through the virtual IP every half second, so the
    write outage of a failover is measured, not estimated."""

    def __init__(self, timeline: Timeline):
        self.timeline = timeline
        self.ok = None
        self.last_ok = None
        self.outage_start = None
        self.last_outage = None  # seconds of the last completed outage
        self.server = None
        self.writes = 0
        self.failures = 0

    def run(self) -> None:
        conn = None
        while True:
            started = now()
            try:
                if conn is None or conn.closed:
                    conn = db.connect(timeout=1)
                with conn.cursor() as cur:
                    cur.execute("INSERT INTO heartbeat (server) VALUES (inet_server_addr()) RETURNING server")
                    server = str(cur.fetchone()[0])
                conn.commit()
                self.writes += 1
                if self.ok is False and self.outage_start:
                    self.last_outage = started - self.outage_start
                    self.timeline.add("recovered", f"Writes through the virtual IP succeed again, after "
                                      f"{self.last_outage:.1f}s without them", "postgres")
                    self.outage_start = None
                self.ok, self.last_ok, self.server = True, started, server
            except Exception as e:  # noqa: BLE001 - any failure is an outage
                self.failures += 1
                if conn is not None:
                    try:
                        conn.close()
                    except Exception:  # noqa: BLE001
                        pass
                conn = None
                if self.ok is not False:
                    self.outage_start = self.last_ok or started
                    self.timeline.add("outage", f"Writes through the virtual IP fail ({e.__class__.__name__})",
                                      "postgres")
                self.ok = False
            time.sleep(max(0.0, 0.5 - (now() - started)))

    def snapshot(self) -> dict:
        ok, outage = self.ok, None
        if ok is False and self.outage_start:
            outage = now() - self.outage_start
        elif ok and self.last_ok and now() - self.last_ok > 1.5:
            # A write still waiting on a dead server has not failed yet, but
            # nothing has been written for three heartbeats.
            ok, outage = False, now() - self.last_ok
        return {"ok": ok, "server": self.server, "writes": self.writes, "failures": self.failures,
                "currentOutage": outage, "lastOutage": self.last_outage}


class Collector:
    def __init__(self):
        try:
            config.load_incluster_config()
        except config.ConfigException:
            config.load_kube_config()
        self.core = client.CoreV1Api()
        self.custom = client.CustomObjectsApi()
        self.batch = client.BatchV1Api()
        self.ec2 = boto3.client("ec2", region_name=REGION)
        self.timeline = Timeline()
        self.probe = Probe(self.timeline)
        self.state: dict = {}
        self.prev: dict = {}
        self.lock = threading.Lock()
        self.pkey = None
        if os.path.exists(SSH_KEY):
            with open(SSH_KEY) as f:
                self.pkey = paramiko.Ed25519Key.from_private_key(io.StringIO(f.read()))
        self.evict_seen: set = set()
        self.kill: dict | None = None  # the last kill: when, how, which host
        self.saved = None
        self.restore()

    # ---------- sources ----------

    def ssh(self, host_ip: str, command: str, timeout: float = 4.0, check: bool = False) -> str:
        """Run command on a host and return its output. With check, a non-zero
        exit status raises, with the command's error output."""
        ssh = paramiko.SSHClient()
        ssh.set_missing_host_key_policy(paramiko.AutoAddPolicy())  # noqa: S507 - demo hosts
        ssh.connect(host_ip, username=SSH_USER, pkey=self.pkey, timeout=timeout, banner_timeout=timeout,
                    auth_timeout=timeout, look_for_keys=False, allow_agent=False)
        try:
            _, out, err = ssh.exec_command(command, timeout=timeout)
            output = out.read().decode()
            if check and (status := out.channel.recv_exit_status()) != 0:
                raise RuntimeError(f"`{command}` exited {status}: {err.read().decode().strip()[-300:]}")
            return output
        finally:
            ssh.close()

    def pacemaker(self) -> dict:
        """crm_mon from the first host that answers, plus the eviction agent's
        log lines from every host that does."""
        result = {"reachableFrom": None, "nodes": {}, "primary": None, "standbys": [], "vip": None,
                  "evict": None, "fencing": [], "failed": []}
        for name, ip in HOSTS.items():
            try:
                xml = self.ssh(ip, "sudo crm_mon --output-as=xml -1 --include=all 2>/dev/null")
                if "<pacemaker-result" not in xml:
                    continue
            except Exception:  # noqa: BLE001 - try the next host
                continue
            result["reachableFrom"] = name
            root = ET.fromstring(xml)
            for node in root.iter("node"):
                if node.get("online") is not None:
                    result["nodes"][node.get("name")] = {
                        "online": node.get("online") == "true", "standby": node.get("standby") == "true",
                        "unclean": node.get("unclean") == "true"}
            for res in root.iter("resource"):
                rid, role = res.get("id"), res.get("role")
                nodes = [n.get("name") for n in res.findall("node")]
                if rid == "pgsql":
                    for n in nodes:
                        if role == "Promoted":
                            result["primary"] = n
                        elif role in ("Unpromoted", "Started"):
                            result["standbys"].append(n)
                elif rid == "pg-vip-ip" and nodes:
                    result["vip"] = nodes[0]
                elif rid == "caplv-evict" and nodes:
                    result["evict"] = nodes[0]
            for f in root.iter("fence_event"):
                result["fencing"].append({"target": f.get("target"), "action": f.get("action"),
                                          "status": f.get("status"), "completed": f.get("completed")})
            for f in root.iter("failure"):
                result["failed"].append({"resource": f.get("op_key"), "node": f.get("node"),
                                         "reason": f.get("exitreason")})
            break
        # The agent's own log of what it did, with the hosts' timestamps.
        for name, ip in HOSTS.items():
            try:
                lines = self.ssh(ip, "sudo journalctl --since '-30min' -o short-unix --no-pager "
                                 "| grep -E 'evict\\(caplv-evict\\).*(Destroyed VM|Cordoned|Uncordoned|"
                                 "Deleted Machine)' | tail -20", timeout=3)
            except Exception:  # noqa: BLE001
                continue
            for line in lines.splitlines():
                if line in self.evict_seen:
                    continue
                self.evict_seen.add(line)
                try:
                    at = float(line.split()[0])
                except ValueError:
                    continue
                text = line.split("INFO: ", 1)[-1].strip()
                self.timeline.add("evict", f"{name}: {text}", "pacemaker", at=at, unique=True)
        return result

    def kube(self) -> dict:
        out = {"hosts": {}, "machines": [], "nodes": {}, "md": {}, "job": {}, "pods": [], "workloads": []}
        for h in self.custom.list_namespaced_custom_object(
                "infrastructure.cluster.x-k8s.io", "v1alpha1", NS_CAPLV, "libvirthosts")["items"]:
            out["hosts"][h["metadata"]["name"]] = {
                "cordoned": bool(h["spec"].get("unschedulable")),
                "consumer": (h["spec"].get("consumerRef") or {}).get("name"),
                "ready": bool(h.get("status", {}).get("ready")),
            }
        lms = {m["metadata"]["name"]: m for m in self.custom.list_namespaced_custom_object(
            "infrastructure.cluster.x-k8s.io", "v1alpha1", NS_CAPLV, "libvirtmachines")["items"]}
        for m in self.custom.list_namespaced_custom_object(
                "cluster.x-k8s.io", "v1beta2", NS_CAPLV, "machines")["items"]:
            if m["metadata"].get("labels", {}).get("cluster.x-k8s.io/deployment-name") != MD_NAME:
                continue
            lm = lms.get(m["spec"]["infrastructureRef"]["name"], {})
            infra = {c["type"]: c for c in lm.get("status", {}).get("conditions", [])}
            out["machines"].append({
                "name": m["metadata"]["name"],
                "phase": m.get("status", {}).get("phase"),
                "node": (m.get("status", {}).get("nodeRef") or {}).get("name"),
                "host": (lm.get("status", {}).get("hostRef") or {}).get("name"),
                "deleting": bool(m["metadata"].get("deletionTimestamp")),
                "waiting": (infra.get("HostAssigned", {}).get("reason")
                            if infra.get("HostAssigned", {}).get("status") == "False" else None),
                "created": m["metadata"]["creationTimestamp"],
            })
        md = self.custom.get_namespaced_custom_object("cluster.x-k8s.io", "v1beta2", NS_CAPLV,
                                                      "machinedeployments", MD_NAME)
        out["md"] = {"replicas": md["spec"].get("replicas"),
                     "ready": md.get("status", {}).get("readyReplicas", 0),
                     "available": md.get("status", {}).get("availableReplicas", 0)}
        for n in self.core.list_node().items:
            ready = next((c.status == "True" for c in n.status.conditions if c.type == "Ready"), False)
            out["nodes"][n.metadata.name] = {
                "ready": ready,
                "scavenged": n.metadata.labels.get("caplv.demo/capacity") == "scavenged",
                "unschedulable": bool(n.spec.unschedulable),
            }
        try:
            job = self.batch.read_namespaced_job(JOB_NAME, NS_DEMO)
            out["job"] = {"exists": True, "completions": job.spec.completions, "parallelism": job.spec.parallelism,
                          "succeeded": job.status.succeeded or 0, "active": job.status.active or 0,
                          "failed": job.status.failed or 0, "suspended": bool(job.spec.suspend),
                          "complete": any(c.type == "Complete" and c.status == "True"
                                          for c in (job.status.conditions or []))}
        except client.ApiException:
            out["job"] = {"exists": False}
        for p in self.core.list_namespaced_pod(NS_DEMO, label_selector=f"job-name={JOB_NAME}").items:
            out["pods"].append({"name": p.metadata.name, "node": p.spec.node_name, "phase": p.status.phase,
                                "index": p.metadata.annotations.get("batch.kubernetes.io/job-completion-index"),
                                "deleting": bool(p.metadata.deletion_timestamp)})
        try:
            for w in self.custom.list_namespaced_custom_object("kueue.x-k8s.io", "v1beta1", NS_DEMO,
                                                               "workloads")["items"]:
                conds = {c["type"]: c["status"] for c in w.get("status", {}).get("conditions", [])}
                out["workloads"].append({"name": w["metadata"]["name"], "admitted": conds.get("Admitted") == "True",
                                         "finished": conds.get("Finished") == "True",
                                         "queue": w["spec"].get("queueName")})
        except client.ApiException:
            pass
        return out

    def postgres(self) -> dict:
        out = {"reachable": False}
        try:
            with db.connect(timeout=2) as conn, conn.cursor() as cur:
                cur.execute("SELECT host(inet_server_addr()), pg_is_in_recovery()")
                out["server"], out["inRecovery"] = cur.fetchone()
                cur.execute("SELECT count(*), count(DISTINCT node) FROM chunks")
                out["rows"], out["embeddingNodes"] = cur.fetchone()
                cur.execute("SELECT node, count(*) FROM chunks GROUP BY node ORDER BY 2 DESC")
                out["byNode"] = dict(cur.fetchall())
                cur.execute("SELECT count(*) FROM chunks WHERE embedded_at > now() - interval '30 seconds'")
                out["rate"] = round(cur.fetchone()[0] / 30.0, 1)
                cur.execute("SELECT client_addr::text, application_name, state, sync_state, "
                            "coalesce(extract(epoch FROM replay_lag), 0) FROM pg_stat_replication")
                out["replicas"] = [{"addr": r[0], "name": r[1], "state": r[2], "sync": r[3], "lag": float(r[4])}
                                   for r in cur.fetchall()]
                out["reachable"] = True
        except Exception as e:  # noqa: BLE001
            out["error"] = e.__class__.__name__
        return out

    def aws(self) -> dict:
        key, value = TAG.split("=", 1)
        out = {}
        resp = self.ec2.describe_instances(Filters=[{"Name": f"tag:{key}", "Values": [value]}])
        for r in resp["Reservations"]:
            for i in r["Instances"]:
                if i["State"]["Name"] == "terminated":
                    continue
                name = next((t["Value"] for t in i.get("Tags", []) if t["Key"] == "Name"), i["InstanceId"])
                out[i.get("PrivateIpAddress", name)] = {
                    "id": i["InstanceId"], "name": name, "state": i["State"]["Name"],
                    "type": i["InstanceType"], "publicIp": i.get("PublicIpAddress"),
                    "secondaryIps": [a["PrivateIpAddress"] for n in i.get("NetworkInterfaces", [])
                                     for a in n.get("PrivateIpAddresses", []) if not a.get("Primary")]}
        return out

    # ---------- the loop ----------

    def collect(self) -> None:
        state = {"at": now()}
        for name, fn in (("pacemaker", self.pacemaker), ("kube", self.kube),
                         ("postgres", self.postgres), ("aws", self.aws)):
            try:
                state[name] = fn()
            except Exception as e:  # noqa: BLE001 - one source failing must not stop the rest
                state[name] = {"error": f"{e.__class__.__name__}: {e}"}
        self.diff(state)
        with self.lock:
            self.state = state

    def diff(self, s: dict) -> None:
        """Turn changes between two snapshots into timeline events."""
        p, t = self.prev, self.timeline
        if not p:
            self.prev = s
            return
        pm, ppm = s.get("pacemaker", {}), p.get("pacemaker", {})
        if pm.get("primary") and pm.get("primary") != ppm.get("primary"):
            t.add("promote", f"Pacemaker promoted PostgreSQL on {pm['primary']}", "pacemaker")
        elif ppm.get("primary") and not pm.get("primary") and pm.get("reachableFrom"):
            t.add("demote", f"No PostgreSQL primary (was {ppm['primary']})", "pacemaker")
        for node, info in pm.get("nodes", {}).items():
            before = ppm.get("nodes", {}).get(node)
            if before and before["online"] and not info["online"]:
                t.add("lost", f"Pacemaker lost node {node}", "pacemaker")
            if before and not before["online"] and info["online"]:
                t.add("joined", f"Node {node} rejoined the Pacemaker cluster", "pacemaker")
        for f in pm.get("fencing", []):
            key = (f["target"], f["completed"])
            if key not in self.evict_seen and f.get("status") == "success":
                self.evict_seen.add(key)
                try:
                    at = datetime.fromisoformat(f["completed"].replace("Z", "+00:00")).timestamp()
                except (AttributeError, ValueError):
                    at = None
                # The fence device turns Pacemaker's "reboot" into a power off
                # (pcmk_reboot_action=off), so a fenced primary stays down.
                t.add("fence", f"fence_aws fenced {f['target']}: powered off its EC2 instance", "pacemaker",
                      at=at, unique=True)
        if pm.get("vip") and pm.get("vip") != ppm.get("vip"):
            t.add("vip", f"Virtual IP now on {pm['vip']}", "pacemaker")

        k, pk = s.get("kube", {}), p.get("kube", {})
        for name, h in k.get("hosts", {}).items():
            before = pk.get("hosts", {}).get(name)
            if before and before["cordoned"] != h["cordoned"]:
                t.add("cordon", f"LibvirtHost {name} {'cordoned' if h['cordoned'] else 'uncordoned'}", "caplv")
        before_m = {m["name"]: m for m in pk.get("machines", [])}
        for m in k.get("machines", []):
            b = before_m.get(m["name"])
            if b is None:
                t.add("machine", f"MachineDeployment created Machine {m['name']}", "capi")
            elif m["deleting"] and not b["deleting"]:
                t.add("machine", f"Machine {m['name']} is being deleted (it was on {m['host']})", "capi")
            elif m["host"] and m["host"] != b["host"]:
                t.add("machine", f"Machine {m['name']} claimed host {m['host']}", "caplv")
            elif m["phase"] != b["phase"] and m["phase"] == "Running":
                t.add("machine", f"Machine {m['name']} is Running on {m['host']}", "capi")
        now_m = {m["name"] for m in k.get("machines", [])}
        for name in before_m:
            if name not in now_m:
                t.add("machine", f"Machine {name} is gone", "capi")
        for name, n in k.get("nodes", {}).items():
            b = pk.get("nodes", {}).get(name)
            if b is None:
                t.add("node", f"Node {name} joined OpenShift", "openshift")
            elif b["ready"] and not n["ready"]:
                t.add("node", f"Node {name} is NotReady", "openshift")
            elif not b["ready"] and n["ready"]:
                t.add("node", f"Node {name} is Ready", "openshift")
        for name in pk.get("nodes", {}):
            if name not in k.get("nodes", {}):
                t.add("node", f"Node {name} was removed from OpenShift", "openshift")

        a, pa = s.get("aws", {}), p.get("aws", {})
        for ip, inst in a.items() if isinstance(a, dict) else []:
            b = pa.get(ip) if isinstance(pa, dict) else None
            if b and b["state"] != inst["state"]:
                t.add("aws", f"EC2 {inst['name']} is {inst['state']}", "aws")
        self.prev = s

    # ---------- surviving a restart ----------

    def restore(self) -> None:
        try:
            cm = self.core.read_namespaced_config_map(STATE_CONFIGMAP, NS_DEMO)
            saved = json.loads((cm.data or {}).get("state", "{}"))
        except (client.ApiException, ValueError):
            return
        self.kill = saved.get("kill")
        self.timeline.events = saved.get("timeline", [])
        self.saved = json.dumps(saved, sort_keys=True)

    def persist(self) -> None:
        with self.timeline.lock:
            state = json.dumps({"kill": self.kill, "timeline": list(self.timeline.events)}, sort_keys=True)
        if state == self.saved:
            return
        body = client.V1ConfigMap(metadata=client.V1ObjectMeta(name=STATE_CONFIGMAP), data={"state": state})
        try:
            try:
                self.core.replace_namespaced_config_map(STATE_CONFIGMAP, NS_DEMO, body)
            except client.ApiException as e:
                if e.status != 404:
                    raise
                self.core.create_namespaced_config_map(NS_DEMO, body)
            self.saved = state
        except client.ApiException:
            pass  # tried again on the next pass

    def run(self) -> None:
        threading.Thread(target=self.probe.run, daemon=True).start()
        while True:
            started = now()
            self.collect()
            self.persist()
            time.sleep(max(0.2, 1.5 - (now() - started)))

    def snapshot(self) -> dict:
        with self.lock:
            s = dict(self.state)
        s["probe"] = self.probe.snapshot()
        s["kill"] = self.kill
        s["timeline"] = self.timeline.since(now() - 3600)
        s["hostIps"] = HOSTS
        return s
