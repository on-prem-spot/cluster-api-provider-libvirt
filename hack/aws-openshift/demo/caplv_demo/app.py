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

"""The demo's front end: an interactive narrative over live state."""

import os
import threading
import time
from pathlib import Path

from fastapi import FastAPI, HTTPException
from fastapi.responses import FileResponse
from fastapi.staticfiles import StaticFiles
from kubernetes import client
from pydantic import BaseModel

from caplv_demo import db
from caplv_demo.state import HOSTS, JOB_NAME, NS_CAPLV, NS_DEMO, Collector

STATIC = Path(__file__).parent / "static"
IMAGE = os.environ.get("IMAGE", "image-registry.openshift-image-registry.svc:5000/caplv-demo/caplv-demo:latest")
SHARDS = int(os.environ.get("SHARDS", "24"))
PARALLELISM = int(os.environ.get("PARALLELISM", "6"))
QUEUE = os.environ.get("QUEUE", "embed")

app = FastAPI(title="CAPLV demo")
collector = Collector()
model = {"embedder": None, "error": None}
recovery = {"running": False, "steps": []}


@app.on_event("startup")
def start() -> None:
    with db.connect(timeout=10) as conn:
        db.init(conn)
    threading.Thread(target=collector.run, daemon=True).start()

    def load():
        try:
            from fastembed import TextEmbedding

            model["embedder"] = TextEmbedding(db.MODEL, cache_dir=os.environ.get("FASTEMBED_CACHE_PATH"), local_files_only=True)
        except Exception as e:  # noqa: BLE001
            model["error"] = str(e)

    threading.Thread(target=load, daemon=True).start()


@app.get("/")
def index():
    return FileResponse(STATIC / "index.html")


app.mount("/static", StaticFiles(directory=STATIC), name="static")


@app.get("/api/config")
def demo_config():
    return {
        "console": os.environ.get("CONSOLE_URL", ""),
        "region": os.environ.get("AWS_REGION", "us-east-2"),
        "tag": os.environ.get("AWS_TAG", "caplv-e2e=caplv-sno"),
        "caplvNamespace": NS_CAPLV, "demoNamespace": NS_DEMO, "job": JOB_NAME,
        "vip": os.environ.get("PGHOST", ""), "model": db.MODEL, "dimensions": db.DIMENSIONS,
        "shards": SHARDS, "parallelism": PARALLELISM, "corpusChunks": corpus_size(),
        "hosts": HOSTS,
    }


_corpus = {}


def corpus_size() -> int:
    if "n" not in _corpus:
        import gzip

        try:
            with gzip.open(os.environ.get("CORPUS", "/opt/corpus/k8s-docs.jsonl.gz"), "rt") as f:
                _corpus["n"] = sum(1 for _ in f)
        except OSError:
            _corpus["n"] = 0
    return _corpus["n"]


@app.get("/api/state")
def state():
    s = collector.snapshot()
    s["recovery"] = recovery
    s["searchReady"] = model["embedder"] is not None
    return s


# ---------- the embedding run ----------

@app.post("/api/run")
def start_run():
    batch = client.BatchV1Api()
    try:
        batch.read_namespaced_job(JOB_NAME, NS_DEMO)
        raise HTTPException(409, "An embedding run already exists; reset it first")
    except client.ApiException as e:
        if e.status != 404:
            raise
    env = [client.V1EnvVar(name=k, value=v) for k, v in {
        "SHARDS": str(SHARDS), "MODEL": db.MODEL, "DIMENSIONS": str(db.DIMENSIONS),
        "PGHOST": os.environ.get("PGHOST", ""), "PGDATABASE": "demo", "PGUSER": "demo",
        "FASTEMBED_CACHE_PATH": os.environ.get("FASTEMBED_CACHE_PATH", "/opt/models"),
    }.items()]
    env += [
        client.V1EnvVar(name="PGPASSWORD", value_from=client.V1EnvVarSource(
            secret_key_ref=client.V1SecretKeySelector(name="postgres", key="password"))),
        client.V1EnvVar(name="NODE_NAME", value_from=client.V1EnvVarSource(
            field_ref=client.V1ObjectFieldSelector(field_path="spec.nodeName"))),
        client.V1EnvVar(name="POD_NAME", value_from=client.V1EnvVarSource(
            field_ref=client.V1ObjectFieldSelector(field_path="metadata.name"))),
    ]
    job = client.V1Job(
        metadata=client.V1ObjectMeta(name=JOB_NAME, labels={"kueue.x-k8s.io/queue-name": QUEUE}),
        spec=client.V1JobSpec(
            completions=SHARDS, parallelism=PARALLELISM, completion_mode="Indexed", suspend=True,
            backoff_limit_per_index=6, max_failed_indexes=SHARDS,
            template=client.V1PodTemplateSpec(
                metadata=client.V1ObjectMeta(labels={"app": "embed"}),
                spec=client.V1PodSpec(
                    restart_policy="Never",
                    # Leave a lost worker quickly rather than after five minutes.
                    tolerations=[client.V1Toleration(key=k, operator="Exists", effect="NoExecute",
                                                     toleration_seconds=10)
                                 for k in ("node.kubernetes.io/not-ready", "node.kubernetes.io/unreachable")],
                    containers=[client.V1Container(
                        name="embed", image=IMAGE, command=["python", "-m", "caplv_demo.embed"], env=env,
                        resources=client.V1ResourceRequirements(
                            requests={"cpu": "1500m", "memory": "3Gi"}, limits={"memory": "4Gi"}))]))))
    batch.create_namespaced_job(NS_DEMO, job)
    collector.timeline.add("run", f"Started embedding run: {SHARDS} shards, up to {PARALLELISM} pods at once, "
                           f"queued in Kueue queue '{QUEUE}'", "kueue")
    return {"ok": True}


@app.post("/api/reset")
def reset_run():
    try:
        client.BatchV1Api().delete_namespaced_job(JOB_NAME, NS_DEMO, propagation_policy="Background")
    except client.ApiException as e:
        if e.status != 404:
            raise
    with db.connect() as conn, conn.cursor() as cur:
        cur.execute("TRUNCATE chunks")
        conn.commit()
    # A fresh start for the next telling: no kill, and a timeline from here.
    collector.kill = None
    if not recovery["running"]:
        recovery["steps"] = []
    with collector.timeline.lock:
        collector.timeline.events.clear()
    collector.timeline.add("run", "Reset: deleted the embedding run and emptied the vector table", "kueue")
    return {"ok": True}


# ---------- search ----------

@app.get("/api/search")
def search(q: str):
    if model["embedder"] is None:
        raise HTTPException(503, model["error"] or "The embedding model is still loading")
    started = time.time()
    vector = next(iter(model["embedder"].query_embed([q])))
    literal = "[" + ",".join(f"{x:.6f}" for x in vector) + "]"
    with db.connect() as conn, conn.cursor() as cur:
        cur.execute("SELECT host(inet_server_addr()), pg_is_in_recovery(), count(*) FROM chunks")
        server, recovering, total = cur.fetchone()
        cur.execute("SELECT title, url, left(content, 360), node, shard, embedding <=> %s::vector AS d "
                    "FROM chunks ORDER BY embedding <=> %s::vector LIMIT 5", (literal, literal))
        rows = cur.fetchall()
    return {"server": server, "inRecovery": recovering, "searched": total, "ms": round((time.time() - started) * 1000),
            "results": [{"title": r[0], "url": r[1], "text": r[2], "node": r[3], "shard": r[4],
                         "similarity": round(1 - float(r[5]), 3)} for r in rows]}


# ---------- failure and recovery ----------

class Kill(BaseModel):
    method: str  # "aws" (power off the EC2 instance) or "panic" (crash the kernel)


@app.post("/api/kill")
def kill(req: Kill):
    s = collector.snapshot()
    primary = s.get("pacemaker", {}).get("primary")
    if not primary:
        raise HTTPException(409, "There is no PostgreSQL primary to kill right now")
    ip = HOSTS[primary]
    instance = s.get("aws", {}).get(ip, {})
    rows = s.get("postgres", {}).get("rows")
    if req.method == "aws":
        # Force alone still lets the OS shut down cleanly first, which would
        # hand Pacemaker an orderly failover; SkipOsShutdown powers it off.
        collector.ec2.stop_instances(InstanceIds=[instance["id"]], Force=True, SkipOsShutdown=True)
        how = f"powered off EC2 instance {instance['id']} without an OS shutdown, like pulling the plug"
    elif req.method == "panic":
        def crash():
            try:
                collector.ssh(ip, "sudo sh -c 'echo 1 >/proc/sys/kernel/sysrq; echo c >/proc/sysrq-trigger'",
                              timeout=3)
            except Exception:  # noqa: BLE001 - the connection dies with the kernel
                pass
        threading.Thread(target=crash, daemon=True).start()
        how = "crashed its kernel (sysrq c)"
    else:
        raise HTTPException(400, "method must be aws or panic")
    collector.kill = {"at": time.time(), "method": req.method, "host": primary, "ip": ip,
                      "instance": instance.get("id"), "rowsBefore": rows}
    collector.timeline.add("kill", f"KILLED the PostgreSQL primary {primary}: {how}", "you")
    return {"ok": True, "host": primary}


@app.post("/api/recover")
def recover():
    if recovery["running"]:
        raise HTTPException(409, "Recovery is already running")
    k = collector.kill
    if not k:
        raise HTTPException(409, "Nothing to recover: no host has been killed")
    recovery.update(running=True, steps=[])
    threading.Thread(target=_recover, args=(k["host"], k["ip"], k["instance"]), daemon=True).start()
    return {"ok": True}


def _step(text: str) -> None:
    recovery["steps"].append({"at": time.time(), "text": text})
    collector.timeline.add("recover", text, "recovery")


def _recover(node: str, ip: str, instance: str) -> None:
    vip = os.environ.get("PGHOST", "")
    try:
        state = collector.ec2.describe_instances(InstanceIds=[instance])["Reservations"][0]["Instances"][0]
        if state["State"]["Name"] in ("stopped", "stopping"):
            collector.ec2.get_waiter("instance_stopped").wait(InstanceIds=[instance])
            collector.ec2.start_instances(InstanceIds=[instance])
            _step(f"Started EC2 instance {instance}")
        collector.ec2.get_waiter("instance_running").wait(InstanceIds=[instance])
        while True:
            try:
                collector.ssh(ip, "true", timeout=3)
                break
            except Exception:  # noqa: BLE001
                time.sleep(3)
        _step(f"{node} is up; waiting for it to rejoin the Pacemaker cluster")
        while True:
            out = collector.ssh(ip, "sudo crm_mon -1 2>/dev/null | grep -E '^ *\\* Online:|^ *Online:' || true")
            if node in out:
                break
            time.sleep(3)
        # The old primary may be ahead of the new one, so it is not allowed to
        # rejoin as it is: its data is replaced with a copy of the new primary.
        _step(f"Re-seeding PostgreSQL on {node} from the new primary (pg_basebackup through {vip})")
        collector.ssh(ip, f"sudo pcs node standby {node} --wait=120", timeout=150, check=True)
        collector.ssh(ip, "sudo sh -c 'rm -rf /var/lib/pgsql/data /var/lib/pgsql/tmp/PGSQL.lock && "
                      f"runuser -u postgres -- pg_basebackup -h {vip} -U replicator -D /var/lib/pgsql/data "
                      "-X stream -c fast'", timeout=600, check=True)
        # Its failed start (the lock) left a fail count that keeps it stopped.
        collector.ssh(ip, f"sudo pcs resource cleanup pgsql node={node} && sudo pcs node unstandby {node}",
                      timeout=60, check=True)
        while "HS:" not in collector.ssh(ip, f"sudo crm_attribute -N {node} -l reboot -n pgsql-status -G -q "
                                             "2>/dev/null || true"):
            time.sleep(3)
        _step(f"{node} rejoined as a PostgreSQL standby")
        # The eviction agent never stopped there (the host died), so lift its cordon.
        client.CustomObjectsApi().patch_namespaced_custom_object(
            "infrastructure.cluster.x-k8s.io", "v1alpha1", NS_CAPLV, "libvirthosts", node,
            {"spec": {"unschedulable": False}})
        _step(f"Uncordoned LibvirtHost {node}: CAPLV may place a worker there again")
    except Exception as e:  # noqa: BLE001
        _step(f"Recovery failed: {e}")
    finally:
        recovery["running"] = False
