// The demo front end: a narrative in chapters on the left, live state on the
// right. /api/state is polled every 1.5s; chapters render once and then fill
// in their live elements on every poll, so inputs and open answers survive.
"use strict";

let C = {}; // /api/config
let S = {}; // /api/state
let current = 0;

const $ = (sel) => document.querySelector(sel);
const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
const fmt = (n) => (n == null ? "–" : Number(n).toLocaleString());
const secs = (s) => (s == null ? "–" : `${s.toFixed(1)}s`);

// ---------- console links ----------

const ocp = (path) => `${C.console}${path}`;
const aws = (frag) => `https://${C.region}.console.aws.amazon.com/ec2/home?region=${C.region}#${frag}`;
const L = {
  nodes: () => ocp("/k8s/cluster/nodes"),
  node: (n) => ocp(`/k8s/cluster/nodes/${n}`),
  machines: () => ocp(`/k8s/ns/${C.caplvNamespace}/cluster.x-k8s.io~v1beta2~Machine`),
  md: () => ocp(`/k8s/ns/${C.caplvNamespace}/cluster.x-k8s.io~v1beta2~MachineDeployment/pg-ha-workers`),
  hosts: () => ocp(`/k8s/ns/${C.caplvNamespace}/infrastructure.cluster.x-k8s.io~v1alpha1~LibvirtHost`),
  host: (n) => ocp(`/k8s/ns/${C.caplvNamespace}/infrastructure.cluster.x-k8s.io~v1alpha1~LibvirtHost/${n}/yaml`),
  job: () => ocp(`/k8s/ns/${C.demoNamespace}/jobs/${C.job}`),
  pods: () => ocp(`/k8s/ns/${C.demoNamespace}/pods`),
  workloads: () => ocp(`/k8s/ns/${C.demoNamespace}/kueue.x-k8s.io~v1beta1~Workload`),
  clusterQueue: () => ocp("/k8s/cluster/kueue.x-k8s.io~v1beta1~ClusterQueue/embed"),
  flavor: () => ocp("/k8s/cluster/kueue.x-k8s.io~v1beta1~ResourceFlavor/scavenged"),
  events: () => ocp(`/k8s/ns/${C.caplvNamespace}/events`),
  caplv: () => ocp("/k8s/ns/caplv-system/deployments/caplv-controller-manager"),
  instances: () => aws(`Instances:tag:${C.tag}`),
  instance: (id) => aws(`InstanceDetails:instanceId=${id}`),
  iamRole: () => "https://us-east-1.console.aws.amazon.com/iam/home#/roles/details/caplv-sno-pacemaker",
  routes: () => `https://${C.region}.console.aws.amazon.com/vpcconsole/home?region=${C.region}#RouteTables:`,
};
function links(items) {
  return `<div class="links">${items
    .filter(Boolean)
    .map(([src, text, href]) => `<a href="${href}" target="_blank" rel="noopener"><span class="src ${src}">${src === "ocp" ? "OpenShift" : "AWS"}</span>${esc(text)}</a>`)
    .join("")}</div>`;
}

// ---------- derived state ----------

function hostNames() {
  return Object.keys(C.hosts || {}).sort();
}
function view(name) {
  const ip = C.hosts[name];
  const inst = (S.aws || {})[ip] || {};
  const pm = S.pacemaker || {};
  const node = (pm.nodes || {})[name];
  const k = S.kube || {};
  const lh = (k.hosts || {})[name] || {};
  const machine = (k.machines || []).find((m) => m.host === name && !m.deleting);
  const rep = ((S.postgres || {}).replicas || []).find((r) => r.name === name);
  const role = pm.primary === name ? "primary" : (pm.standbys || []).includes(name) ? "standby" : null;
  const up = inst.state === "running" && (!node || node.online);
  return { name, ip, inst, node, lh, machine, rep, role, up, worker: machine && machine.node ? (k.nodes || {})[machine.node] : null };
}
function primary() {
  return (S.pacemaker || {}).primary;
}
function workerNodes() {
  return Object.entries((S.kube || {}).nodes || {}).filter(([, n]) => n.scavenged);
}
function afterKill(kind, match) {
  const k = S.kill;
  if (!k) return null;
  return (S.timeline || []).find((e) => e.at >= k.at - 1 && e.kind === kind && (!match || e.text.includes(match)));
}
function sinceKill(e) {
  return e && S.kill ? e.at - S.kill.at : null;
}

// ---------- the live panel ----------

function renderHosts() {
  const html = hostNames()
    .map((name) => {
      const v = view(name);
      const cls = v.role === "primary" ? "primary" : !v.up ? "down" : "";
      const role = !v.up
        ? `<span class="badge b-down">DOWN</span>`
        : v.role === "primary"
        ? `<span class="badge b-primary">PRIMARY</span>`
        : v.role === "standby"
        ? `<span class="badge b-standby">STANDBY</span>${v.rep && v.rep.sync === "sync" ? ` <span class="badge b-sync">SYNC</span>` : ""}`
        : `<span class="badge b-standby">no postgres</span>`;
      const vm = v.machine
        ? v.machine.node && v.worker && v.worker.ready
          ? `<span class="badge b-vm">worker ready</span>`
          : v.machine.waiting
          ? `<span class="badge b-wait">${esc(v.machine.waiting)}</span>`
          : `<span class="badge b-wait">${esc(v.machine.phase || "starting")}</span>`
        : `<span class="badge b-novm">none</span>`;
      return `<div class="host ${cls}">
        <div class="name">${esc(name)}</div>
        <div class="row"><span>EC2</span><span>${esc(v.inst.state || "?")}</span></div>
        <div class="row"><span>PostgreSQL</span><span>${role}</span></div>
        <div class="row"><span>VIP</span><span>${(S.pacemaker || {}).vip === name ? "<b>10.0.1.250</b>" : "–"}</span></div>
        <div class="row"><span>Worker VM</span><span>${vm}</span></div>
        <div class="row"><span>CAPLV</span><span>${v.lh.cordoned ? `<span class="badge b-cordon">CORDONED</span>` : "schedulable"}</span></div>
      </div>`;
    })
    .join("");
  setHTML("#hosts", html);
  const workers = workerNodes();
  const md = (S.kube || {}).md || {};
  setHTML(
    "#openshift",
    `OpenShift: <b>${workers.filter(([, n]) => n.ready).length}</b> borrowed worker node(s) ready · MachineDeployment <b>${fmt(md.ready)}/${fmt(md.replicas)}</b> ready` +
      (S.pacemaker && S.pacemaker.reachableFrom ? "" : ` · <span style="color:var(--red)">Pacemaker unreachable</span>`)
  );
}

function renderProbe() {
  const p = S.probe || {};
  const ok = p.ok === true;
  const html = `<div class="probe">
    <div class="dot ${p.ok == null ? "" : ok ? "ok" : "bad"}"></div>
    <div>
      <div class="big">${p.ok == null ? "starting…" : ok ? `Writing OK · served by ${esc(serverName(p.server))}` : `WRITES FAILING · ${secs(p.currentOutage)}`}</div>
      <div class="sub">${fmt(p.writes)} writes, ${fmt(p.failures)} failed attempts${p.lastOutage != null ? ` · last outage <b>${secs(p.lastOutage)}</b>` : ""}</div>
    </div></div>`;
  setHTML("#probe", html);
}
function serverName(ip) {
  const name = Object.entries(C.hosts || {}).find(([, v]) => v === ip || ip === C.vip);
  if (ip === C.vip) return `the virtual IP (${primary() || "?"})`;
  return name ? name[0] : ip || "?";
}

function renderProgress() {
  const pg = S.postgres || {};
  const job = (S.kube || {}).job || {};
  const total = C.corpusChunks || 1;
  const rows = pg.rows || 0;
  const pct = Math.min(100, (100 * rows) / total);
  const pods = (S.kube || {}).pods || [];
  const running = pods.filter((p) => p.phase === "Running" && !p.deleting);
  const byNode = Object.entries(pg.byNode || {})
    .map(([n, c]) => `<span>${esc(short(n))}: ${fmt(c)}</span>`)
    .join("");
  const status = !job.exists
    ? "not started"
    : job.complete
    ? "complete"
    : job.suspended
    ? "queued in Kueue (suspended until admitted)"
    : `${running.length} pod(s) embedding`;
  setHTML(
    "#progress",
    `<div class="bar"><div style="width:${pct}%"></div></div>
     <div class="progress-row"><span>${fmt(rows)} / ${fmt(total)} chunks in pgvector (${pct.toFixed(1)}%)</span><span>${pg.rate ? `${pg.rate}/s` : ""}</span></div>
     <div class="progress-row"><span>Job: ${esc(status)}</span><span>${job.exists ? `${fmt(job.succeeded)}/${fmt(job.completions)} shards done` : ""}</span></div>
     ${byNode ? `<div class="nodes-split">${byNode}</div>` : ""}`
  );
}
function short(node) {
  return String(node).replace(/^caplv-workers-caplv-sno-/, "");
}

function renderTimeline() {
  const k = S.kill;
  const events = (S.timeline || []).slice(-120).reverse();
  setHTML("#timeline-hint", k ? `seconds since the kill` : "what each layer did");
  const html = events
    .map((e) => {
      const t = k && e.at >= k.at - 60 ? `${e.at - k.at >= 0 ? "+" : ""}${(e.at - k.at).toFixed(1)}s` : new Date(e.at * 1000).toLocaleTimeString();
      return `<li class="${e.kind === "kill" ? "kill" : ""}"><span class="t">${t}</span><span class="layer L-${e.layer}">${esc(e.layer)}</span><span>${esc(e.text)}</span></li>`;
    })
    .join("");
  setHTML("#timeline", html || `<li><span></span><span></span><span class="muted">Nothing yet.</span></li>`);
}

const cache = {};
function setHTML(sel, html) {
  if (cache[sel] === html) return;
  cache[sel] = html;
  const el = $(sel);
  if (el) el.innerHTML = html;
}
function live(id, html) {
  setHTML(`#${id}`, html);
}

// ---------- chapters ----------

const chapters = [
  {
    nav: "The idea",
    html: () => `
      <div class="kicker">Chapter 1</div>
      <h2>Highly available databases keep servers idle</h2>
      <p class="lede">A PostgreSQL cluster built for failover has one primary doing the work, and standbys that replay its changes and wait. Most of their CPU and memory waits with them.</p>
      <p>This demo runs on three RHEL servers with the High Availability Add-On. One is the PostgreSQL primary; two are standbys. Each server has 8 vCPUs and 32 GB of memory.</p>
      <div id="c1-facts"></div>
      <p>The idea: <b>lend the standbys' spare capacity to OpenShift</b>, as worker nodes that run batch jobs, and <b>take it back the instant the database needs it</b>, when a standby is promoted after a failure.</p>
      <div class="diagram">
        <div class="box"><b>RHEL HA</b><br>Pacemaker decides where the primary runs, and fences failed servers</div>
        <div class="box"><b>CAPLV</b><br>Cluster API provider that turns a RHEL host into an OpenShift worker VM</div>
        <div class="box"><b>Kueue</b><br>Queues batch work onto whatever borrowed capacity exists</div>
      </div>
      <div class="callout">The handshake is a Pacemaker resource: wherever PostgreSQL is about to be promoted, it first <b>kills that server's worker VM</b> and <b>tells CAPLV to stay away</b>. The database never waits for Kubernetes.</div>
      <p class="muted">Keep an eye on the panel on the right: it is live, refreshed from Pacemaker, OpenShift, PostgreSQL and AWS every couple of seconds.</p>
      ${links([["aws", "The three servers in EC2", L.instances()], ["ocp", "Nodes in OpenShift", L.nodes()]])}`,
    update() {
      const standbys = hostNames().filter((n) => view(n).role === "standby").length;
      const cap = Object.values((S.kube || {}).hosts || {}).length;
      live(
        "c1-facts",
        `<div class="facts">
          <div class="fact"><div class="v">${hostNames().length}</div><div class="k">RHEL HA servers</div></div>
          <div class="fact"><div class="v">${standbys}</div><div class="k">standbys right now</div></div>
          <div class="fact good"><div class="v">${standbys * 6} vCPU</div><div class="k">lent to OpenShift (6 per standby; 2 kept for PostgreSQL)</div></div>
          <div class="fact good"><div class="v">${standbys * 22} GB</div><div class="k">memory lent (8 GB kept for PostgreSQL)</div></div>
        </div>`
      );
    },
  },
  {
    nav: "The database",
    html: () => `
      <div class="kicker">Chapter 2</div>
      <h2>A RHEL High Availability PostgreSQL cluster</h2>
      <p class="lede">Pacemaker runs PostgreSQL 16 on all three servers and decides which one is primary.</p>
      <div id="c2-table"></div>
      <h3>What protects the data</h3>
      <ul>
        <li><b>Synchronous replication.</b> A commit is acknowledged only once a standby has it. Pacemaker promotes that standby, so a failover loses no acknowledged write.</li>
        <li><b>Fencing.</b> Before promoting anyone, Pacemaker makes sure the old primary is really gone: <code>fence_aws</code> powers it off through the EC2 API, using an IAM role that can stop only these instances. Two primaries at once (split brain) cannot happen.</li>
        <li><b>A virtual IP.</b> Clients, including the batch jobs, connect to <code>${esc(C.vip)}</code>, a secondary private address that Pacemaker moves to the primary's network interface (the <code>awsvip</code> agent) and configures there (<code>IPaddr2</code>).</li>
      </ul>
      <div class="callout">The green light in the panel is a writer in this app inserting a row through the virtual IP twice a second. When we break things in chapter 6, it measures the outage.</div>
      ${links([
        ["aws", "Servers and their state", L.instances()],
        primary() ? ["aws", `Primary's network interface (VIP is a secondary IP)`, L.instance(view(primary()).inst.id)] : null,
        ["aws", "The fencing IAM role", L.iamRole()],
      ])}`,
    update() {
      const pm = S.pacemaker || {};
      const pg = S.postgres || {};
      const reps = (pg.replicas || []).map((r) => `${esc(r.name)} (${esc(r.sync)}, lag ${r.lag.toFixed(2)}s)`).join(", ");
      live(
        "c2-table",
        `<table class="kv">
          <tr><td>Primary (Pacemaker)</td><td><b>${esc(pm.primary || "none")}</b></td></tr>
          <tr><td>Standbys</td><td>${esc((pm.standbys || []).join(", ") || "–")}</td></tr>
          <tr><td>Replication (from the primary)</td><td>${reps || "–"}</td></tr>
          <tr><td>Virtual IP ${esc(C.vip)} is on</td><td>${esc(pm.vip || "–")}</td></tr>
          <tr><td>Fencing</td><td>fence_aws, power off</td></tr>
          <tr><td>Status read from</td><td>${esc(pm.reachableFrom || "unreachable")} (crm_mon)</td></tr>
        </table>`
      );
    },
  },
  {
    nav: "Lending capacity",
    html: () => `
      <div class="kicker">Chapter 3</div>
      <h2>CAPLV turns the standbys into OpenShift workers</h2>
      <p class="lede">CAPLV is a Cluster API infrastructure provider for libvirt. Each RHEL server is a <code>LibvirtHost</code>; CAPLV runs one RHCOS VM on it, which boots and joins OpenShift as a worker node.</p>
      <ol class="steps">
        <li>A <b>MachineDeployment</b> asks for <b>2</b> workers. Each Machine claims a free <code>LibvirtHost</code> by label.</li>
        <li>CAPLV defines the VM over SSH, sized to the server minus what PostgreSQL keeps (2 vCPU, 8 GB), with a static address on a routed network.</li>
        <li>The VM boots RHCOS with OpenShift's own worker config and joins the cluster; CAPLV approves its certificates.</li>
      </ol>
      <div class="callout warn"><b>Why no worker on the primary?</b> The Pacemaker eviction resource runs where the primary runs, and it <b>cordons</b> that server's LibvirtHost (<code>spec.unschedulable: true</code>). A cordoned host gets no VM, and CAPLV will not start one that stops there.</div>
      <div id="c3-table"></div>
      ${links([
        ["ocp", "MachineDeployment pg-ha-workers", L.md()],
        ["ocp", "Machines", L.machines()],
        ["ocp", "LibvirtHosts (see the Cordoned column)", L.hosts()],
        ["ocp", "Nodes", L.nodes()],
        ["aws", "VPC routes to each server's VM subnet", L.routes()],
      ])}`,
    update() {
      const rows = hostNames()
        .map((n) => {
          const v = view(n);
          return `<tr><td>${esc(n)} (${v.role || "down"})</td><td>${
            v.lh.cordoned ? "cordoned — no worker" : v.machine ? `Machine <code>${esc(v.machine.name)}</code> → node <code>${esc(short(v.machine.node || "booting…"))}</code>` : "free"
          }</td></tr>`;
        })
        .join("");
      live("c3-table", `<table class="kv">${rows}</table>`);
    },
  },
  {
    nav: "Batch work",
    html: () => `
      <div class="kicker">Chapter 4</div>
      <h2>Put them to work: embeddings into pgvector</h2>
      <p class="lede">The workers compute vector embeddings of the Kubernetes documentation and store them in the same HA PostgreSQL, which doubles as the vector database (pgvector, from RHEL AppStream).</p>
      <ul>
        <li><b>${fmt(C.corpusChunks)}</b> chunks of text, embedded with <code>${esc(C.model)}</code> (${C.dimensions} dimensions) on CPU.</li>
        <li>An Indexed <b>Job</b> of <b>${C.shards}</b> shards, at most <b>${C.parallelism}</b> pods at a time, submitted to the <b>Kueue</b> queue <code>embed</code>.</li>
        <li>Kueue's ResourceFlavor <code>scavenged</code> sends the pods only to nodes labelled <code>caplv.demo/capacity=scavenged</code>: the borrowed workers, never the control plane.</li>
        <li>Each pod skips chunks already in the table, so a pod that dies with its worker resumes where it stopped.</li>
      </ul>
      <div class="actions">
        <button id="start" class="blue">Start the embedding run</button>
        <button id="reset" class="ghost">Reset (delete the run, empty the table)</button>
        <span id="c4-status" class="muted"></span>
      </div>
      <div id="c4-facts"></div>
      ${links([
        ["ocp", "Kueue workloads", L.workloads()],
        ["ocp", "ClusterQueue quota", L.clusterQueue()],
        ["ocp", "ResourceFlavor scavenged", L.flavor()],
        ["ocp", "Embedding pods", L.pods()],
        ["ocp", "The Job", L.job()],
      ])}`,
    bind() {
      $("#start").onclick = () => post("/api/run", "Embedding run submitted to Kueue");
      $("#reset").onclick = () => confirm("Delete the run and empty the vector table?") && post("/api/reset", "Reset");
    },
    update() {
      const k = S.kube || {};
      const job = k.job || {};
      const pods = k.pods || [];
      const byNode = {};
      pods.filter((p) => p.phase === "Running").forEach((p) => (byNode[p.node] = (byNode[p.node] || 0) + 1));
      const wl = (k.workloads || [])[0];
      $("#start").disabled = !!job.exists;
      live("c4-status", job.exists ? (wl ? (wl.admitted ? "Kueue admitted the workload." : "Waiting for Kueue to admit it…") : "Submitted.") : "");
      live(
        "c4-facts",
        `<div class="facts">
          <div class="fact"><div class="v">${fmt((S.postgres || {}).rows)}</div><div class="k">vectors stored</div></div>
          <div class="fact"><div class="v">${(S.postgres || {}).rate || 0}/s</div><div class="k">embedding rate (last 30s)</div></div>
          <div class="fact"><div class="v">${pods.filter((p) => p.phase === "Running").length}</div><div class="k">pods running</div></div>
          <div class="fact"><div class="v">${fmt(job.succeeded || 0)}/${fmt(job.completions || C.shards)}</div><div class="k">shards complete</div></div>
        </div>
        <p class="muted">Pods by node: ${Object.entries(byNode).map(([n, c]) => `${esc(short(n))} × ${c}`).join(", ") || "–"}</p>`
      );
    },
  },
  {
    nav: "Search",
    html: () => `
      <div class="kicker">Chapter 5</div>
      <h2>Ask the documentation</h2>
      <p class="lede">The vectors are real. Search them: your question is embedded with the same model and PostgreSQL finds the nearest chunks with pgvector's cosine distance operator <code>&lt;=&gt;</code>.</p>
      <div class="search"><input id="q" placeholder="How do I drain a node before maintenance?"><button id="go" class="blue">Search</button></div>
      <div class="suggest">
        ${["How do I drain a node before maintenance?", "What happens when a node becomes unreachable?", "How do PodDisruptionBudgets work?", "How does the scheduler use taints and tolerations?"]
          .map((s) => `<button>${esc(s)}</button>`)
          .join("")}
      </div>
      <div id="results"></div>
      <p class="muted">Try it again after the failover in the next chapter: the same vectors answer from the new primary.</p>`,
    bind() {
      const run = async () => {
        const q = $("#q").value.trim() || $("#q").placeholder;
        $("#results").innerHTML = `<p class="muted">Searching…</p>`;
        const r = await fetch(`/api/search?q=${encodeURIComponent(q)}`);
        const d = await r.json();
        if (!r.ok) {
          $("#results").innerHTML = `<div class="callout warn">${esc(d.detail)}</div>`;
          return;
        }
        $("#results").innerHTML =
          `<p class="muted">Searched ${fmt(d.searched)} vectors in ${d.ms} ms, served by PostgreSQL on <b>${esc(serverName(d.server))}</b>${d.inRecovery ? " (a standby)" : " (the primary)"}.</p>` +
          d.results
            .map(
              (x) => `<div class="result"><span class="sim">${x.similarity}</span><b><a href="${esc(x.url)}" target="_blank">${esc(x.title)}</a></b>
              <div>${esc(x.text)}…</div><div class="meta">embedded by worker ${esc(short(x.node))}, shard ${x.shard}</div></div>`
            )
            .join("");
      };
      $("#go").onclick = run;
      $("#q").onkeydown = (e) => e.key === "Enter" && run();
      document.querySelectorAll(".suggest button").forEach((b) => (b.onclick = () => (($("#q").value = b.textContent), run())));
    },
    update() {},
  },
  {
    nav: "Pull the plug",
    html: () => `
      <div class="kicker">Chapter 6</div>
      <h2>Kill the PostgreSQL primary</h2>
      <p class="lede">Let's fail the server running the primary, mid-batch, and watch every layer react.</p>
      <h3>What should happen</h3>
      <ol class="steps" id="c6-steps"></ol>
      <div class="actions">
        <button id="kill-aws" class="danger">Pull the plug: power off the primary's EC2 instance</button>
        <button id="kill-panic" class="ghost">Or: crash its kernel</button>
      </div>
      <div id="c6-facts"></div>
      <div id="c6-links"></div>`,
    bind() {
      const kill = (method) => {
        const p = primary();
        if (!p) return toast("There is no primary right now.");
        const how = method === "aws" ? "power off its EC2 instance, with no OS shutdown" : "crash its kernel";
        if (confirm(`Kill ${p}, the PostgreSQL primary? This will ${how}.`)) post("/api/kill", `Killed ${p}`, { method });
      };
      $("#kill-aws").onclick = () => kill("aws");
      $("#kill-panic").onclick = () => kill("panic");
    },
    update() {
      const k = S.kill;
      const p = primary();
      $("#kill-aws").disabled = $("#kill-panic").disabled = !!k || !p;
      const lost = afterKill("lost");
      const fence = afterKill("fence");
      const destroyed = afterKill("evict", "Destroyed VM");
      const cordoned = afterKill("evict", "Cordoned");
      const promoted = afterKill("promote");
      const vip = afterKill("vip");
      const deleted = afterKill("evict", "Deleted Machine") || afterKill("machine", "being deleted");
      const created = afterKill("machine", "created Machine");
      const recovered = afterKill("recovered");
      const newPrimary = promoted ? promoted.text.replace(/.* on /, "") : "a standby";
      const step = (done, text) => `<li class="${done ? "done" : ""}">${text}${done ? `<span class="when">+${sinceKill(done).toFixed(1)}s</span>` : ""}</li>`;
      live(
        "c6-steps",
        [
          step(k && afterKill("kill"), `The primary${k ? ` (${esc(k.host)})` : p ? ` (${esc(p)})` : ""} dies. Writes through the virtual IP start failing.`),
          step(lost, "Pacemaker on the survivors notices it is gone (corosync)."),
          step(fence, "Pacemaker fences it: fence_aws makes sure the EC2 instance is powered off."),
          step(destroyed, `On ${esc(newPrimary)}, the eviction resource <b>hard-kills the worker VM</b> (virsh destroy): its CPU and memory are free at once.`),
          step(cordoned, `…and <b>cordons</b> that LibvirtHost, so CAPLV does not start the VM again.`),
          step(promoted, `Pacemaker <b>promotes PostgreSQL</b> on ${esc(newPrimary)}; it was the synchronous standby, so no acknowledged write is lost.`),
          step(vip, "The virtual IP moves to the new primary."),
          step(recovered, "Writes succeed again."),
          step(deleted, "In the background, the dead worker's Machine is deleted; OpenShift reschedules its pods."),
          step(created, "The MachineDeployment creates a replacement worker. With one standby busy and the old primary dead, there is no free host yet: it waits (chapter 7)."),
        ].join("")
      );
      if (!k) {
        live("c6-facts", `<div class="callout">The embedding run should be going (chapter 4) to see the batch work survive. Current primary: <b>${esc(p || "none")}</b>.</div>`);
      } else {
        const pg = S.postgres || {};
        const out = (S.probe || {}).lastOutage;
        const lostRows = k.rowsBefore != null && pg.rows != null ? Math.max(0, k.rowsBefore - pg.rows) : null;
        live(
          "c6-facts",
          `<div class="facts">
            <div class="fact ${out != null ? "good" : "bad"}"><div class="v">${out != null ? secs(out) : secs((S.probe || {}).currentOutage)}</div><div class="k">${out != null ? "write outage, measured" : "writes failing for"}</div></div>
            <div class="fact"><div class="v">${promoted ? `+${sinceKill(promoted).toFixed(1)}s` : "…"}</div><div class="k">kill → new primary</div></div>
            <div class="fact good"><div class="v">${destroyed && promoted ? `${(promoted.at - destroyed.at).toFixed(1)}s` : "…"}</div><div class="k">VM killed before promotion</div></div>
            <div class="fact ${lostRows === 0 ? "good" : "warn"}"><div class="v">${lostRows == null ? "…" : lostRows === 0 ? "0" : lostRows}</div><div class="k">vectors lost (${fmt(k.rowsBefore)} before → ${fmt(pg.rows)} now)</div></div>
          </div>`
        );
      }
      const dead = k ? view(k.host) : null;
      const np = p ? view(p) : null;
      live(
        "c6-links",
        links([
          dead && dead.inst.id ? ["aws", `Killed server ${dead.name}: instance state`, L.instance(dead.inst.id)] : null,
          np && np.inst.id ? ["aws", `New primary ${np.name}: VIP on its interface`, L.instance(np.inst.id)] : null,
          ["ocp", "Machines (old one deleted, replacement waiting)", L.machines()],
          ["ocp", "LibvirtHosts (who is cordoned)", L.hosts()],
          ["ocp", "Events in caplv-workers", L.events()],
          ["ocp", "Embedding pods (rescheduled)", L.pods()],
        ])
      );
    },
  },
  {
    nav: "Recovery",
    html: () => `
      <div class="kicker">Chapter 7</div>
      <h2>Bring the old primary back</h2>
      <p class="lede">The killed server cannot simply rejoin: it may hold writes the new primary never saw, so Pacemaker's PostgreSQL agent refuses to start it (it leaves a lock file behind for exactly this). It has to be re-seeded from the new primary.</p>
      <ol class="steps">
        <li>Start the EC2 instance; Pacemaker starts on boot and the server rejoins the cluster.</li>
        <li>Replace its PostgreSQL data with a fresh copy of the new primary (<code>pg_basebackup</code> through the virtual IP), clear the lock, and let Pacemaker start it as a standby.</li>
        <li>Uncordon its LibvirtHost. (The eviction resource would have done this on a clean switchover; here the server died with it.)</li>
        <li>CAPLV sees a free host: the replacement worker that has been waiting since the failover claims it, boots, and joins OpenShift.</li>
      </ol>
      <div class="actions"><button id="recover" class="blue">Recover the old primary</button><span id="c7-status" class="muted"></span></div>
      <ol class="steps" id="c7-progress"></ol>
      <div id="c7-links"></div>`,
    bind() {
      $("#recover").onclick = () => post("/api/recover", "Recovery started");
    },
    update() {
      const r = S.recovery || {};
      $("#recover").disabled = !S.kill || r.running;
      live("c7-status", !S.kill ? "Kill the primary first (chapter 6)." : r.running ? "Recovering…" : r.steps && r.steps.length ? "Done." : "");
      live("c7-progress", (r.steps || []).map((s) => `<li class="done">${esc(s.text)}<span class="when">${new Date(s.at * 1000).toLocaleTimeString()}</span></li>`).join(""));
      const k = S.kill;
      live("c7-links", links([k && view(k.host).inst.id ? ["aws", `${k.host} in EC2`, L.instance(view(k.host).inst.id)] : null, ["ocp", "Machines", L.machines()], ["ocp", "Nodes", L.nodes()]]));
    },
  },
  {
    nav: "Questions",
    html: () => `
      <div class="kicker">Chapter 8</div>
      <h2>Questions people ask</h2>
      <p class="lede">Answers use this demo's live numbers where there are some.</p>
      <div id="qa"></div>`,
    update() {
      const k = S.kill;
      const destroyed = afterKill("evict", "Destroyed VM");
      const promoted = afterKill("promote");
      const out = (S.probe || {}).lastOutage;
      const qa = [
        ["Does the database ever wait for Kubernetes during a failover?",
          `No. The eviction resource kills the worker VM locally, with <code>virsh destroy</code>, before anything else. Its two Kubernetes calls (cordon, delete Machine) have a 5-second limit and the Machine deletion runs in the background. ${destroyed && promoted ? `In the last failover the VM was gone <b>${(promoted.at - destroyed.at).toFixed(1)}s</b> before PostgreSQL was promoted.` : "Pull the plug in chapter 6 to measure it."}`],
        ["Could CAPLV start the VM again on the new primary?",
          "No. The eviction resource cordons the LibvirtHost (<code>spec.unschedulable</code>). On a cordoned host CAPLV creates and starts no VM, not even one that already exists and is stopped. The resource is colocated with the primary, so the cordon follows the primary wherever Pacemaker puts it, and is lifted when the primary moves away."],
        ["Was any data lost?",
          `Replication is synchronous: PostgreSQL acknowledges a commit only once a standby has it, and that standby is the one promoted. ${k && S.postgres && S.postgres.rows != null ? `The vector table had ${fmt(k.rowsBefore)} rows when the primary was killed and has ${fmt(S.postgres.rows)} now.` : ""} Asynchronous replication would be faster but could lose the last commits; the embedding run would survive that too, because it skips only what is stored.`],
        ["How long were writes unavailable?",
          out != null ? `The writer in this app measured <b>${secs(out)}</b> without a successful write. Most of it is detecting the failure (corosync's token timeout) and fencing; promotion and moving the address take a few seconds.` : "Pull the plug and the writer in this app measures it."],
        ["What happened to the batch jobs on the killed worker?",
          "Their pods died with the VM. When the Machine was deleted its Node went away, the Job controller created new pods for those shards, and Kueue kept the workload admitted. Each new pod skips the chunks already in pgvector and carries on. The rate dips while one worker is missing, then recovers when the replacement joins."],
        ["Why fence at all, if the server is already dead?",
          "Pacemaker cannot tell a dead server from a disconnected one. Without fencing, a primary that was only cut off could keep accepting writes while a standby is promoted: two primaries, diverging data. fence_aws powers the instance off through the EC2 API first; its IAM role may stop only this environment's instances."],
        ["What if the Kubernetes API is down when the failover happens?",
          "The database fails over anyway: the VM kill is local and the API calls time out. CAPLV also needs the API to act, so it cannot start the VM again either; when the API returns, the eviction resource's next start (or an operator) applies the cordon."],
        ["How much capacity does this reclaim?",
          `Each standby lends 6 of its 8 vCPUs and 22 of its 32 GB (LibvirtHost <code>reservedResources</code> keeps 2 vCPUs and 8 GB for PostgreSQL). With ${hostNames().filter((n) => view(n).role === "standby").length} standbys that is ${hostNames().filter((n) => view(n).role === "standby").length * 6} vCPUs that would otherwise wait for a failure.`],
        ["Couldn't the worker starve PostgreSQL on the standby?",
          "The VM is sized to what the host has left after the reservation, and the hypervisor enforces it. A standby only replays WAL and serves optional reads. When it becomes the primary, the VM is destroyed before promotion, so the primary never shares the server."],
        ["Why VMs, not containers on the database servers?",
          "Isolation and a clean kill switch. The worker is a real OpenShift node (RHCOS, managed by the Machine Config Operator) that cannot touch PostgreSQL's files or processes, and one command removes all of it. The servers stay plain RHEL HA, with nothing from OpenShift installed on them."],
        ["Why not just drain the worker gracefully?",
          "Draining takes as long as the slowest pod takes to stop, and a database failover should not wait for that. Batch work is designed to be interrupted; the database is not. A graceful drain with a short timeout would be easy to add for workloads that need it."],
        ["Which parts are Red Hat products?",
          "RHEL with the High Availability Add-On (Pacemaker, fence agents, resource agents), PostgreSQL 16 and pgvector from RHEL AppStream, OpenShift, and the Red Hat build of Kueue. CAPLV is an open source Cluster API provider (github.com/on-prem-spot/cluster-api-provider-libvirt)."],
        ["Is this realistic, or a cloud trick?",
          "The servers here are EC2 instances with nested virtualization, so the worker VMs run inside them. On premises they would be bare-metal RHEL servers, fenced through their BMCs (fence_ipmilan, fence_redfish) instead of the EC2 API. Nothing else changes."],
        ["What would production add?",
          "Backups (pgBackRest) and monitoring, a second synchronous standby, a MachineHealthCheck, an automated re-seed of a failed primary, alerting on cordoned hosts, and capacity limits in Kueue that track how many workers actually exist."],
      ];
      const open = new Set([...document.querySelectorAll("#qa details[open]")].map((d) => d.dataset.i));
      live("qa", qa.map(([q, a], i) => `<details class="qa" data-i="${i}" ${open.has(String(i)) ? "open" : ""}><summary>${esc(q)}</summary><div>${a}</div></details>`).join(""));
    },
  },
];

// ---------- plumbing ----------

function show(i) {
  current = Math.max(0, Math.min(chapters.length - 1, i));
  location.hash = String(current + 1);
  Object.keys(cache).filter((k) => !k.startsWith("#hosts") && !["#probe", "#progress", "#timeline", "#openshift", "#timeline-hint"].includes(k)).forEach((k) => delete cache[k]);
  $("#chapter").innerHTML = chapters[current].html();
  chapters[current].bind && chapters[current].bind();
  chapters[current].update();
  document.querySelectorAll("nav button").forEach((b, j) => b.classList.toggle("on", j === current));
  $("#prev").disabled = current === 0;
  $("#next").disabled = current === chapters.length - 1;
  $("#where").textContent = `${current + 1} of ${chapters.length}`;
  $("#story").scrollIntoView({ block: "start" });
}

async function post(url, ok, body) {
  const r = await fetch(url, { method: "POST", headers: { "Content-Type": "application/json" }, body: body ? JSON.stringify(body) : undefined });
  const d = await r.json().catch(() => ({}));
  toast(r.ok ? ok : d.detail || `Failed (${r.status})`);
  poll();
}

function toast(text) {
  const t = $("#toast");
  t.textContent = text;
  t.hidden = false;
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => (t.hidden = true), 3500);
}

async function poll() {
  try {
    S = await (await fetch("/api/state")).json();
  } catch {
    return;
  }
  renderHosts();
  renderProbe();
  renderProgress();
  renderTimeline();
  try {
    chapters[current].update();
  } catch (e) {
    console.error(e);
  }
}

async function main() {
  C = await (await fetch("/api/config")).json();
  $("#chapters").innerHTML = chapters.map((c, i) => `<button><span class="n">${i + 1}</span>${esc(c.nav)}</button>`).join("");
  document.querySelectorAll("nav button").forEach((b, i) => (b.onclick = () => show(i)));
  $("#prev").onclick = () => show(current - 1);
  $("#next").onclick = () => show(current + 1);
  document.addEventListener("keydown", (e) => {
    if (e.target.tagName === "INPUT") return;
    if (e.key === "ArrowRight") show(current + 1);
    if (e.key === "ArrowLeft") show(current - 1);
  });
  await poll();
  show((parseInt(location.hash.slice(1), 10) || 1) - 1);
  setInterval(poll, 1500);
}
main();
