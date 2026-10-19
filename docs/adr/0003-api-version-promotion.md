# API version promotion

Status: proposed. Plan for issue #28; it also decides when the conversion
scaffold from issue #35 is built.

## Context

The CAPLV API is `infrastructure.cluster.x-k8s.io/v1alpha1`. Under the
Kubernetes API conventions an alpha version may break at any time, and that
holds back adoption. The official providers are at v1beta1 or v1beta2.

The contract CAPLV declares and the one it builds against do not match:

- `metadata.yaml` advertises the CAPI **v1beta2** contract, and the CRDs carry
  only the `cluster.x-k8s.io/v1beta2` contract label. CAPI v1.11 and later
  therefore read CAPLV objects with v1beta2 field shapes.
- The code builds against the CAPI **v1.9.11** Go module, which predates
  v1beta2. It imports `sigs.k8s.io/cluster-api/api/v1beta1` and the
  transitional `GetV1Beta2Conditions`/`SetV1Beta2Conditions` accessors.

CAPI deprecated the v1beta1 contract in v1.11. Its migration notes plan to
remove the compatibility layer (tentatively August 2026, on track for v1.16),
after which v1beta1-contract providers stop working with current CAPI.

The v1beta2 contract fields CAPLV now populates on v1alpha1 are:

| Contract field | Object | State |
| --- | --- | --- |
| `status.initialization.provisioned` | LibvirtCluster, LibvirtMachine | Set alongside `status.ready` |
| `status.failureDomains` (list of `{name, controlPlane, attributes}`) | LibvirtCluster | One entry per LibvirtHost |
| `status.failureDomain` | LibvirtMachine | The machine's host |
| `status.conditions` as `[]metav1.Condition` | all | Already the storage shape |

Known remaining gaps:

- LibvirtMachine reports no `Ready` condition. CAPI falls back to
  `initialization.provisioned` for the Machine's `InfrastructureReady`
  condition, so this is cosmetic, but the contract expects the condition.
- `status.ready`, `status.failureReason` and `status.failureMessage` are not
  part of the v1beta2 contract. They remain on v1alpha1 for compatibility.
- The provider does not allocate hosts or addresses, so a template cannot back
  more than one replica. That limits ClusterClass and KubeadmControlPlane use,
  which v1beta2 users will expect.

## Decision

Skip v1beta1. Following the advice on #28, the next API version is
**`v1beta2`**, introduced together with a move to a CAPI release that ships the
v1beta2 contract. Going through v1beta1 first would mean two conversions and
two migrations for no user benefit.

Kubernetes does not require contiguous version names, and matching the contract
version makes clear which contract the API was designed against.

### Sequence

1. **Build against the declared contract.** Bump `sigs.k8s.io/cluster-api` to
   the current v1.14.x, adding the separate `sigs.k8s.io/cluster-api/api`
   module, together with the controller-runtime and Kubernetes library
   versions it requires. Port imports from `api/v1beta1` to
   `api/core/v1beta2` and replace the transitional condition accessors with
   the v1beta2 ones. This changes no CAPLV API and can ship on v1alpha1.
2. **Close the contract gaps on v1alpha1** where that is additive: add the
   LibvirtMachine `Ready` condition, and stop relying on CAPI reading
   `failureReason`/`failureMessage`.
3. **Settle the breaking changes before beta.** These are the reasons to cut
   a new version at all; once v1beta2 is served they get much harder to make:
   - remove `status.ready`, `status.failureReason`, `status.failureMessage`
     (or move them under a `status.deprecated` block, as CAPI core did);
   - remove the deprecated `SecretReference.namespace`;
   - decide credential management (#41, a `LibvirtClusterIdentity`) so
     `LibvirtHost.spec.secretRef` is not frozen in its current form;
   - decide host and address allocation: whether `spec.hostRef` becomes
     optional and is resolved from the failure domain CAPI chooses, and how
     static addresses are assigned per machine (e.g. CAPI IPAM);
   - apply the CAPI v1beta2 API conventions (`omitzero` structs instead of
     pointers where the zero value is meaningless, `MinProperties`, explicit
     list types).
4. **Add `v1beta2` as the hub and storage version, with v1alpha1 as a spoke.**
   Scaffold with
   `kubebuilder create webhook --group infrastructure --version v1beta2
   --kind <Kind> --conversion --spoke v1alpha1` for each of the five kinds.
   Implement `ConvertTo`/`ConvertFrom` on v1alpha1, and cover them with
   round-trip fuzz tests (`sigs.k8s.io/cluster-api/util/conversion`). Label
   the CRDs `cluster.x-k8s.io/v1beta2: v1alpha1_v1beta2`, and add a new
   release series to `metadata.yaml`.
5. **Deprecate v1alpha1** one release after v1beta2 ships: mark it
   `+kubebuilder:deprecatedversion`, keep it served, and remove it in a later
   release after storage migration.

### Why the conversion scaffold waits (#35)

#35 asks for `ConvertTo`/`ConvertFrom` on v1alpha1 "as the hub for now". With
one version there is nothing to convert. When v1beta2 arrives it becomes the
hub, so v1alpha1's `Hub()` methods would be deleted again, and a hub-only
scaffold tests no conversion. ADR 0002 already concluded that a single-version
conversion webhook does not establish compatibility. The scaffold is built in
step 4, when there is a real spoke to convert.

## Promotion criteria

v1beta2 ships, and v1alpha1 is deprecated, when:

- the e2e suite (#23) creates, scales within current limits, and deletes a
  cluster against the targeted CAPI release;
- conversion round-trip fuzz tests pass for every kind;
- every kind has validating webhooks (LibvirtMachine and both templates
  already do; LibvirtCluster and LibvirtHost were added for #36);
- the step 3 decisions are made and no further breaking field changes are
  planned;
- the API reference (`docs/api-reference/`) is regenerated for v1beta2.

## Consequences

- Moving to CAPI v1.11+ in step 1 formally drops older CAPI cores. That is
  already true in practice: `metadata.yaml` and the CRD labels only declare
  v1beta2.
- Users get one migration, from v1alpha1 to v1beta2, done by the conversion
  webhook with no manual edits.
- Until step 1 lands, the provider runs on CAPI's v1beta2 contract while
  compiling against v1beta1 types. Contract fields CAPLV populates are mirrored
  by hand in v1alpha1 (`FailureDomain`, the initialization status types),
  rather than imported from CAPI.
