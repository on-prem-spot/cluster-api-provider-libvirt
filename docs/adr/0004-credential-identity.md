# Credential identity

Status: accepted. Decides issue #41 and the credential item of ADR 0003
step 3.

## Context

Each `LibvirtHost` names the Secret holding its SSH private key with
`spec.secretRef`. Since the cross-namespace fix, that Secret must be in the
host's own namespace. This has three costs:

- A fleet sharing one service-account key needs a copy of the Secret in every
  namespace with hosts, and a rotation has to update every copy.
- Whoever can create hosts in a namespace needs the key in that namespace, so
  in practice can read it. Platform teams cannot let tenants register hosts
  without handing them the key.
- `secretRef` would be frozen in this form once v1beta2 is served.

The official providers solve this with an identity resource: CAPV's
`VSphereClusterIdentity`, CAPA's `AWSClusterStaticIdentity` and CAPZ's
`AzureClusterIdentity`.

## Decision

Add a cluster-scoped `LibvirtClusterIdentity`, modelled on CAPV's
`VSphereClusterIdentity`:

- `spec.secretName` names a Secret in the **controller's namespace**, with
  the same `ssh-privatekey` key a `secretRef` Secret has. The controller
  learns its namespace from `POD_NAMESPACE` (set by the Deployment through
  the downward API), overridable with `--identity-namespace`.
- `spec.allowedNamespaces` gates use by namespace, by `list` or by label
  `selector`. Unset allows no namespace and `{}` allows every namespace, as in
  CAPA. Denying by default means an identity is inert until an administrator
  says who may use it.
- `LibvirtHost.spec.identityRef: {name}` references it. Exactly one of
  `secretRef` and `identityRef` must be set, enforced by a CEL rule on the CRD
  and by the webhook. `secretRef` stays: a single host owner with their own key
  should not need cluster-scoped objects.

The controller resolves the identity, checks the namespace and reads the Secret
on every connection, so rotation needs no restart and no host edits. The host
controller watches identities and rechecks the hosts that use one when it
changes. Problems surface as the host's `Ready=False` / `ConnectionFailed`
message, and the webhook warns about a missing or non-admitting identity at
admission time.

### Alternatives

- **Namespaced identity** (CAPZ style). Sharing across namespaces would then
  need cross-namespace references, which is the escalation the `secretRef`
  namespace fix removed.
- **`identityRef` with a `kind`** (CAPV style, `kind: Secret` or
  `kind: VSphereClusterIdentity`). There is one identity kind and `secretRef`
  already covers the Secret case, so a `kind` would be a field with one useful
  value. If a second identity kind arrives, adding an optional `kind` that
  defaults to `LibvirtClusterIdentity` stays compatible.
- **Known hosts on the identity.** The issue suggested holding known-hosts
  data too. Host keys are per host and already pinned by
  `spec.hostKeyFingerprint`, which a shared identity cannot express better.

## Consequences

- No identity status or controller. Nothing about an identity is meaningful
  apart from a host using it, and the host already reports the result. A
  status can be added later without breaking anything.
- An allowed namespace can use the key against any SSH endpoint it names. SSH
  public-key authentication does not reveal the private key to the server, but
  it does let the namespace log in as the identity's account wherever that key
  is trusted. The documentation says to scope `allowedNamespaces` accordingly.
- The manager needs `get;list;watch` on `libvirtclusteridentities` and on
  `namespaces`, the latter for label selectors.
- `clusterctl move` support followed in ADR 0005, using CAPV's approach: the
  `clusterctl.cluster.x-k8s.io/move-hierarchy` label and an owner reference
  from the Secret to the identity.
