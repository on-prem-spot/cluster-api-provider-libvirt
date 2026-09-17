/*
Copyright 2026 Anthony Green.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	gossh "golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	infrav1 "github.com/atgreen/caplv/api/v1alpha1"
	"github.com/atgreen/caplv/internal/bootartifacts"
	"github.com/atgreen/caplv/internal/ignition"
	"github.com/atgreen/caplv/internal/imagecache"
	"github.com/atgreen/caplv/internal/iso"
	"github.com/atgreen/caplv/internal/libvirt"
	"github.com/atgreen/caplv/internal/scope"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/patch"
)

const (
	hostNotReadyRequeueInterval      = 30 * time.Second
	bootstrapNotReadyRequeueInterval = 10 * time.Second
	cleanupStalledRequeueInterval    = 60 * time.Second
	nodeJoinRequeueInterval          = 15 * time.Second
	memoryMBToKBMultiplier           = 1024
)

const defaultMaxConcurrentReconciles = 50

// LibvirtMachineReconciler reconciles a LibvirtMachine object.
type LibvirtMachineReconciler struct {
	client.Client
	Scheme               *runtime.Scheme
	SSHClientFactory     SSHClientFactory
	LibvirtClientFactory LibvirtClientFactory
	ISOBuilder           iso.Builder
	// BootArtifactsResolver fetches kernel+initramfs when a LibvirtCluster
	// has spec.bootArtifacts set. Resolved bytes are cached in artifactCache
	// keyed by a stable digest of the source spec.
	BootArtifactsResolver bootartifacts.Resolver
	// BaseImageCache stages qcow2 base images locally on the controller (per
	// LibvirtCluster.spec.baseImage) and hands their paths to the per-host
	// upload step. Nil disables the baseImage feature.
	BaseImageCache *imagecache.Cache
	// MaxConcurrentReconciles is the number of machines reconciled in parallel.
	// Each reconcile targets a different libvirt host over its own SSH connection.
	// Default: 50.
	MaxConcurrentReconciles int

	artifactCache sync.Map

	// baseImageHostUpload coalesces concurrent uploads of the same staged
	// image to the same host. Key: "<host>|<volume-sha256>".
	baseImageHostUpload sync.Map
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirtmachines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirtmachines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirtmachines/finalizers,verbs=update
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirthosts,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirtclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines;machines/status,verbs=get;list;watch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters;clusters/status,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;patch;update;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile handles the lifecycle of a LibvirtMachine.
func (r *LibvirtMachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("machine", req.Name, "namespace", req.Namespace)
	log.Info("Reconciling LibvirtMachine")
	ctx = logf.IntoContext(ctx, log)

	// Fetch LibvirtMachine.
	libvirtMachine := &infrav1.LibvirtMachine{}
	if err := r.Get(ctx, req.NamespacedName, libvirtMachine); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Create patch helper for deferred status patch.
	patchHelper, err := patch.NewHelper(libvirtMachine, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}
	defer func() {
		if patchErr := patchHelper.Patch(ctx, libvirtMachine); patchErr != nil {
			log.Error(patchErr, "Failed to patch LibvirtMachine")
		}
	}()

	// Fetch owner Machine.
	machine, err := util.GetOwnerMachine(ctx, r.Client, libvirtMachine.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, err
	}
	if machine == nil {
		log.Info("Waiting for Machine controller to set OwnerRef on LibvirtMachine")
		return ctrl.Result{}, nil
	}

	// Fetch owner Cluster.
	cluster, err := util.GetClusterFromMetadata(ctx, r.Client, libvirtMachine.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, err
	}

	// If cluster is paused, return without requeueing.
	if annotations.IsPaused(cluster, libvirtMachine) {
		log.Info("LibvirtMachine or owning Cluster is paused, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	// Fetch LibvirtCluster from Cluster's InfrastructureRef.
	libvirtCluster := &infrav1.LibvirtCluster{}
	infraRef := cluster.Spec.InfrastructureRef
	if infraRef == nil {
		return ctrl.Result{}, fmt.Errorf("cluster %s/%s has no InfrastructureRef", cluster.Namespace, cluster.Name)
	}
	infraKey := types.NamespacedName{
		Namespace: libvirtMachine.Namespace,
		Name:      infraRef.Name,
	}
	if err := r.Get(ctx, infraKey, libvirtCluster); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get LibvirtCluster: %w", err)
	}

	// Fetch LibvirtHost from spec.hostRef.
	libvirtHost := &infrav1.LibvirtHost{}
	hostKey := types.NamespacedName{
		Namespace: libvirtMachine.Namespace,
		Name:      libvirtMachine.Spec.HostRef.Name,
	}
	if err := r.Get(ctx, hostKey, libvirtHost); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get LibvirtHost: %w", err)
	}

	// Dispatch to delete or normal reconciliation.
	if !libvirtMachine.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, libvirtMachine, machine, cluster, libvirtCluster, libvirtHost)
	}
	return r.reconcileNormal(ctx, libvirtMachine, machine, cluster, libvirtCluster, libvirtHost)
}

// reconcileCtx holds shared state passed between reconcileNormal sub-steps.
type reconcileCtx struct {
	libvirtMachine *infrav1.LibvirtMachine
	libvirtCluster *infrav1.LibvirtCluster
	machine        *clusterv1.Machine
	libvirtHost    *infrav1.LibvirtHost
	machineScope   *scope.MachineScope
	libvirtClient  libvirt.Client

	// Computed values populated during reconciliation.
	resolvedVCPUs    int32
	resolvedMemoryMB int32
	storagePool      string
	baseImagePool    string
	domainName       string
	rootDiskVolume   string
	bootstrapISO     string
	nvramPath        string
	ignitionFilePath string

	// Populated by reconcileAdditionalDisks.
	additionalDiskVolumes []string
	additionalDiskParams  []libvirt.DiskParam

	// Populated by reconcileBootArtifacts when spec.bootArtifacts is set.
	kernelPath    string
	initrdPath    string
	kernelCmdline string
}

// reconcileNormal handles creating and configuring the libvirt domain.
func (r *LibvirtMachineReconciler) reconcileNormal(
	ctx context.Context,
	libvirtMachine *infrav1.LibvirtMachine,
	machine *clusterv1.Machine,
	cluster *clusterv1.Cluster,
	libvirtCluster *infrav1.LibvirtCluster,
	libvirtHost *infrav1.LibvirtHost,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Add finalizer if not present.
	if !controllerutil.ContainsFinalizer(libvirtMachine, infrav1.MachineFinalizer) {
		controllerutil.AddFinalizer(libvirtMachine, infrav1.MachineFinalizer)
	}

	// Check LibvirtHost readiness.
	if !libvirtHost.Status.Ready {
		log.Info("LibvirtHost is not ready, requeueing", "host", libvirtHost.Name)
		apimeta.SetStatusCondition(&libvirtMachine.Status.Conditions, metav1.Condition{
			Type:               infrav1.HostReachableForMachineCondition,
			Status:             metav1.ConditionFalse,
			Reason:             infrav1.ReasonHostNotReady,
			Message:            "LibvirtHost is not ready",
			ObservedGeneration: libvirtMachine.Generation,
		})
		return ctrl.Result{RequeueAfter: hostNotReadyRequeueInterval}, nil
	}

	// Fetch SSH secret and create clients.
	sshClient, libvirtClient, err := r.createClients(ctx, libvirtHost)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create clients: %w", err)
	}
	defer func() { _ = sshClient.Close() }()
	defer func() { _ = libvirtClient.Close() }()

	// Attach logger to VirshClient if applicable.
	if vc, ok := libvirtClient.(*libvirt.VirshClient); ok {
		vc.WithLogger(log)
	}

	// Ensure bootstrap data is available.
	if result, err := r.ensureBootstrapData(ctx, libvirtMachine, machine); result != nil {
		return *result, err
	}

	// Build MachineScope.
	machineScope, err := scope.NewMachineScope(scope.MachineScopeParams{
		Client:         r.Client,
		Cluster:        cluster,
		Machine:        machine,
		LibvirtCluster: libvirtCluster,
		LibvirtMachine: libvirtMachine,
		LibvirtHost:    libvirtHost,
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create machine scope: %w", err)
	}

	// Resolve auto-sizing: if vcpus or memoryMB are zero, use host capacity.
	resolvedVCPUs, resolvedMemoryMB, result := r.resolveAutoSizing(ctx, libvirtMachine, libvirtHost)
	if result != nil {
		return *result, nil
	}

	// Compute artifact names and storage pools.
	storagePool := libvirtMachine.Spec.RootDisk.StoragePool
	baseImagePool := libvirtMachine.Spec.RootDisk.BaseImagePool
	if baseImagePool == "" {
		baseImagePool = storagePool
	}

	// The system daemon's NVRAM directory (/var/lib/libvirt/qemu/nvram) is
	// root-owned; a session daemon runs as the service account and cannot
	// create files there. Leave the path empty on session-mode hosts so
	// libvirt allocates the NVRAM file in its own per-user nvram directory.
	nvramPath := machineScope.NVRAMPath()
	if connURI, _ := libvirt.LocalConnectionURI(libvirtHost.Spec.URI); connURI == libvirt.ConnURISession {
		nvramPath = ""
	}

	rc := &reconcileCtx{
		libvirtMachine:   libvirtMachine,
		libvirtCluster:   libvirtCluster,
		machine:          machine,
		libvirtHost:      libvirtHost,
		machineScope:     machineScope,
		libvirtClient:    libvirtClient,
		resolvedVCPUs:    resolvedVCPUs,
		resolvedMemoryMB: resolvedMemoryMB,
		storagePool:      storagePool,
		baseImagePool:    baseImagePool,
		domainName:       machineScope.DomainName(),
		rootDiskVolume:   machineScope.RootDiskVolumeName(),
		bootstrapISO:     machineScope.BootstrapISOName(),
		nvramPath:        nvramPath,
		ignitionFilePath: machineScope.IgnitionFilePath(),
	}

	// Enrich logger with host and domain context.
	log = log.WithValues("host", libvirtHost.Name, "domain", rc.domainName)
	ctx = logf.IntoContext(ctx, log)

	// Step -3: Session-mode hosts cannot attach machines to libvirt-managed
	// networks — the per-user daemon has no network driver. Catch the mismatch
	// here as a clear, terminal condition instead of letting virsh start fail
	// later with an opaque "Network not found".
	r.preflightSessionMode(ctx, rc)
	if libvirtMachine.Status.FailureReason != nil {
		return ctrl.Result{}, nil
	}

	// Step -2: Verify every libvirt storage pool this machine references exists
	// on the host before any volume work begins. A missing pool otherwise
	// surfaces as an opaque "Storage pool not found" error that retries forever;
	// here it becomes a clear, terminal condition.
	if err := r.preflightPools(ctx, rc); err != nil {
		return ctrl.Result{}, err
	}
	// A terminal preflight failure (e.g. a referenced pool doesn't exist) is not
	// retryable; stop here rather than letting later steps clobber the failure
	// reason with a less actionable one.
	if libvirtMachine.Status.FailureReason != nil {
		return ctrl.Result{}, nil
	}

	// Step -1: Stage the cluster's base image onto this host if needed.
	if err := r.reconcileBaseImage(ctx, rc); err != nil {
		return ctrl.Result{}, err
	}

	// Step 0+1: Ensure ephemeral pool and root disk.
	if err := r.reconcileRootDisk(ctx, rc); err != nil {
		return ctrl.Result{}, err
	}

	// Step 2: Prepare bootstrap artifacts.
	if err := r.reconcileBootstrapArtifacts(ctx, rc); err != nil {
		return ctrl.Result{}, err
	}

	// Step 3: Create additional disks.
	if err := r.reconcileAdditionalDisks(ctx, rc); err != nil {
		return ctrl.Result{}, err
	}

	// Step 3.5: Stage direct-boot kernel/initramfs on the host if requested.
	if err := r.reconcileBootArtifacts(ctx, rc); err != nil {
		return ctrl.Result{}, err
	}

	// Step 4+5: Define and start domain.
	domainInfo, err := r.reconcileDomain(ctx, rc)
	if err != nil {
		return ctrl.Result{}, err
	}
	if domainInfo == nil {
		// reconcileDomain recorded a terminal error on the machine; no domain
		// info is available so skip further status updates and return cleanly.
		return ctrl.Result{}, nil
	}

	// Update status.
	libvirtMachine.Status.Addresses = machineScope.GetAddresses()
	providerID := machineScope.ProviderID()
	libvirtMachine.Spec.ProviderID = &providerID
	artifacts := &infrav1.ManagedArtifacts{
		DomainName:            rc.domainName,
		RootDiskVolume:        rc.rootDiskVolume,
		NVRAMPath:             rc.nvramPath,
		AdditionalDiskVolumes: rc.additionalDiskVolumes,
	}
	switch libvirtMachine.Spec.BootstrapFormat {
	case infrav1.BootstrapFormatIgnition:
		artifacts.IgnitionFile = rc.ignitionFilePath
	case infrav1.BootstrapFormatCloudInit:
		artifacts.BootstrapISO = rc.bootstrapISO
	}
	if libvirtMachine.Spec.RootDisk.EphemeralPool {
		artifacts.EphemeralPoolName = machineScope.EphemeralPoolName()
	}
	libvirtMachine.Status.ManagedArtifacts = artifacts
	libvirtMachine.Status.DomainState = domainInfo.State
	libvirtMachine.Status.DomainUUID = domainInfo.UUID
	libvirtMachine.Status.Ready = true

	apimeta.SetStatusCondition(&libvirtMachine.Status.Conditions, metav1.Condition{
		Type:               infrav1.InfrastructureReadyCondition,
		Status:             metav1.ConditionTrue,
		Reason:             infrav1.ReasonProvisioned,
		Message:            "Machine infrastructure is ready",
		ObservedGeneration: libvirtMachine.Generation,
	})

	log.Info("Machine infrastructure ready", "providerID", providerID)

	// Apply user-declared Node labels and annotations once kubelet has
	// registered the Node. Does not block readiness — the infra is ready
	// when the domain is up; labelling is a follow-on step.
	return r.reconcileNodeLabels(ctx, libvirtMachine, machineScope.DomainName())
}

// reconcileNodeLabels applies spec.nodeLabels and spec.nodeAnnotations to the
// Kubernetes Node that backs this LibvirtMachine. The reconciler runs from
// the controller's own identity (not kubelet), so NodeRestriction does not
// apply and arbitrary label/annotation keys are accepted.
//
// Ownership: CAPLV owns only the keys it has applied, tracked via the
// ManagedNodeLabelsAnnotation / ManagedNodeAnnotationsAnnotation annotations
// on the Node. Keys that disappear from spec on a subsequent reconcile are
// removed; admin-applied keys CAPLV never set are left untouched.
func (r *LibvirtMachineReconciler) reconcileNodeLabels(
	ctx context.Context,
	libvirtMachine *infrav1.LibvirtMachine,
	nodeName string,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	desiredLabels := libvirtMachine.Spec.NodeLabels
	desiredAnnotations := libvirtMachine.Spec.NodeAnnotations

	if len(desiredLabels) == 0 && len(desiredAnnotations) == 0 {
		// Nothing to do; clear any prior condition so observers don't see
		// a stale signal after the user removes all keys.
		apimeta.RemoveStatusCondition(&libvirtMachine.Status.Conditions, infrav1.NodeLabelledCondition)
		return ctrl.Result{}, nil
	}

	node := &corev1.Node{}
	if err := r.Get(ctx, client.ObjectKey{Name: nodeName}, node); err != nil {
		if apierrors.IsNotFound(err) {
			apimeta.SetStatusCondition(&libvirtMachine.Status.Conditions, metav1.Condition{
				Type:               infrav1.NodeLabelledCondition,
				Status:             metav1.ConditionFalse,
				Reason:             infrav1.ReasonNodeNotJoined,
				Message:            fmt.Sprintf("waiting for Node %q to join the cluster", nodeName),
				ObservedGeneration: libvirtMachine.Generation,
			})
			log.Info("Node not yet joined, will retry node labelling", "node", nodeName)
			return ctrl.Result{RequeueAfter: nodeJoinRequeueInterval}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get Node %q: %w", nodeName, err)
	}

	patchBase := client.MergeFrom(node.DeepCopy())

	prevLabelKeys := parseManagedKeys(node.Annotations[infrav1.ManagedNodeLabelsAnnotation])
	prevAnnotationKeys := parseManagedKeys(node.Annotations[infrav1.ManagedNodeAnnotationsAnnotation])

	if node.Labels == nil {
		node.Labels = map[string]string{}
	}
	for _, k := range prevLabelKeys {
		if _, keep := desiredLabels[k]; !keep {
			delete(node.Labels, k)
		}
	}
	maps.Copy(node.Labels, desiredLabels)

	if node.Annotations == nil {
		node.Annotations = map[string]string{}
	}
	for _, k := range prevAnnotationKeys {
		if _, keep := desiredAnnotations[k]; !keep {
			delete(node.Annotations, k)
		}
	}
	maps.Copy(node.Annotations, desiredAnnotations)

	if len(desiredLabels) > 0 {
		node.Annotations[infrav1.ManagedNodeLabelsAnnotation] = formatManagedKeys(desiredLabels)
	} else {
		delete(node.Annotations, infrav1.ManagedNodeLabelsAnnotation)
	}
	if len(desiredAnnotations) > 0 {
		node.Annotations[infrav1.ManagedNodeAnnotationsAnnotation] = formatManagedKeys(desiredAnnotations)
	} else {
		delete(node.Annotations, infrav1.ManagedNodeAnnotationsAnnotation)
	}

	if err := r.Patch(ctx, node, patchBase); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to patch Node %q: %w", nodeName, err)
	}

	log.Info("Applied node labels and annotations",
		"node", nodeName, "labels", len(desiredLabels), "annotations", len(desiredAnnotations))

	apimeta.SetStatusCondition(&libvirtMachine.Status.Conditions, metav1.Condition{
		Type:               infrav1.NodeLabelledCondition,
		Status:             metav1.ConditionTrue,
		Reason:             infrav1.ReasonNodeLabelled,
		Message:            "Node labels and annotations applied",
		ObservedGeneration: libvirtMachine.Generation,
	})

	return ctrl.Result{}, nil
}

// formatManagedKeys serialises the keys of m as a sorted comma-separated
// list for storage in a Node annotation. Sorting keeps the value stable
// across reconciles so we don't churn the Node when spec doesn't change.
func formatManagedKeys(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// parseManagedKeys reverses formatManagedKeys. An empty input yields nil.
func parseManagedKeys(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ensureBootstrapData checks that bootstrap data is available.
// Returns a non-nil result pointer when reconcileNormal should return early.
func (r *LibvirtMachineReconciler) ensureBootstrapData(
	ctx context.Context,
	libvirtMachine *infrav1.LibvirtMachine,
	machine *clusterv1.Machine,
) (*ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if machine.Spec.Bootstrap.DataSecretName == nil {
		log.Info("Bootstrap data not yet available, requeueing")
		apimeta.SetStatusCondition(&libvirtMachine.Status.Conditions, metav1.Condition{
			Type:               infrav1.BootstrapDataReadyCondition,
			Status:             metav1.ConditionFalse,
			Reason:             infrav1.ReasonBootstrapDataNotReady,
			Message:            "Waiting for bootstrap data secret",
			ObservedGeneration: libvirtMachine.Generation,
		})
		return &ctrl.Result{RequeueAfter: bootstrapNotReadyRequeueInterval}, nil
	}

	// Check if the referenced bootstrap secret exists.
	secret := &corev1.Secret{}
	key := types.NamespacedName{
		Namespace: machine.Namespace,
		Name:      *machine.Spec.Bootstrap.DataSecretName,
	}
	if err := r.Get(ctx, key, secret); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Bootstrap secret not found, requeueing", "secret", *machine.Spec.Bootstrap.DataSecretName)
			apimeta.SetStatusCondition(&libvirtMachine.Status.Conditions, metav1.Condition{
				Type:               infrav1.BootstrapDataReadyCondition,
				Status:             metav1.ConditionFalse,
				Reason:             infrav1.ReasonBootstrapDataNotReady,
				Message:            fmt.Sprintf("Bootstrap secret %q not found", *machine.Spec.Bootstrap.DataSecretName),
				ObservedGeneration: libvirtMachine.Generation,
			})
			return &ctrl.Result{RequeueAfter: bootstrapNotReadyRequeueInterval}, nil
		}
		return &ctrl.Result{}, fmt.Errorf("failed to check bootstrap secret: %w", err)
	}

	// Bootstrap data is available. Flip the condition to True so the positive
	// state is observable; otherwise the False condition set on an earlier
	// reconcile (before the secret existed) lingers and misleads operators
	// into thinking the machine is still blocked on bootstrap.
	apimeta.SetStatusCondition(&libvirtMachine.Status.Conditions, metav1.Condition{
		Type:               infrav1.BootstrapDataReadyCondition,
		Status:             metav1.ConditionTrue,
		Reason:             infrav1.ReasonBootstrapDataReady,
		Message:            "Bootstrap data secret is available",
		ObservedGeneration: libvirtMachine.Generation,
	})

	return nil, nil
}

// resolveAutoSizing resolves VCPUs and memory, falling back to host capacity
// when the spec values are zero. Returns a non-nil result pointer when the
// caller should return early.
func (r *LibvirtMachineReconciler) resolveAutoSizing(
	ctx context.Context,
	libvirtMachine *infrav1.LibvirtMachine,
	libvirtHost *infrav1.LibvirtHost,
) (int32, int32, *ctrl.Result) {
	log := logf.FromContext(ctx)

	resolvedVCPUs := libvirtMachine.Spec.Domain.VCPUs
	resolvedMemoryMB := libvirtMachine.Spec.Domain.MemoryMB
	if resolvedVCPUs == 0 || resolvedMemoryMB == 0 {
		if libvirtHost.Status.Capacity == nil {
			log.Info("Host capacity not yet discovered, requeueing")
			return 0, 0, &ctrl.Result{RequeueAfter: hostNotReadyRequeueInterval}
		}
		if resolvedVCPUs == 0 {
			resolvedVCPUs = libvirtHost.Status.Capacity.AvailableVCPUs
		}
		if resolvedMemoryMB == 0 {
			resolvedMemoryMB = libvirtHost.Status.Capacity.AvailableMemoryMB
		}
		if resolvedVCPUs <= 0 || resolvedMemoryMB <= 0 {
			log.Error(fmt.Errorf("insufficient resources: %d vCPUs, %d MB", resolvedVCPUs, resolvedMemoryMB),
				"Terminal error", "operation", "auto-sizing", "reason", infrav1.ReasonStorageInsufficient)
			r.setTerminalError(libvirtMachine, infrav1.ReasonStorageInsufficient,
				fmt.Sprintf("host %s has insufficient available resources: %d vCPUs, %d MB",
					libvirtHost.Name, resolvedVCPUs, resolvedMemoryMB))
			return 0, 0, &ctrl.Result{}
		}
		log.Info("Auto-sized VM from host capacity", "vcpus", resolvedVCPUs, "memoryMB", resolvedMemoryMB)
	}
	return resolvedVCPUs, resolvedMemoryMB, nil
}

// requiredPool is a libvirt storage pool a machine references, paired with the
// terminal reason and human label used if it's missing from the host.
type requiredPool struct {
	name   string
	label  string
	reason string
}

// preflightSessionMode rejects spec/host combinations that cannot work on a
// session-mode (qemu:///session) host. The per-user daemon has no network
// driver, so network.type=network machines would fail at virsh start with an
// opaque "Network not found" that looks retryable; catching it here turns it
// into a terminal, actionable condition. Purely local — no SSH round-trips.
func (r *LibvirtMachineReconciler) preflightSessionMode(ctx context.Context, rc *reconcileCtx) {
	lm := rc.libvirtMachine
	connURI, err := libvirt.LocalConnectionURI(rc.libvirtHost.Spec.URI)
	if err != nil {
		// An unparseable URI already failed client creation before this point.
		return
	}
	if connURI != libvirt.ConnURISession || lm.Spec.Network.Type != infrav1.NetworkTypeNetwork {
		return
	}
	msg := fmt.Sprintf(
		"Host %q uses session-mode libvirt (%s), which cannot attach machines to libvirt-managed networks; use network.type=bridge or a /system host",
		rc.libvirtHost.Name, rc.libvirtHost.Spec.URI)
	logf.FromContext(ctx).Info("Terminal error: network type unsupported on session-mode host",
		"networkType", lm.Spec.Network.Type, "host", rc.libvirtHost.Name)
	r.setTerminalError(lm, infrav1.ReasonNetworkTypeUnsupported, msg)
	apimeta.SetStatusCondition(&lm.Status.Conditions, metav1.Condition{
		Type:               infrav1.InfrastructureReadyCondition,
		Status:             metav1.ConditionFalse,
		Reason:             infrav1.ReasonNetworkTypeUnsupported,
		Message:            msg,
		ObservedGeneration: lm.Generation,
	})
}

// preflightPools verifies that every storage pool this machine references
// already exists on the host before any volume work begins. A missing pool
// otherwise surfaces deep inside vol-create-as / vol-info as an opaque
// "Storage pool not found" error that the reconciler retries forever; catching
// it here turns it into a single, terminal, actionable condition.
//
// Pools that CAPLV provisions itself are intentionally skipped: when
// spec.rootDisk.ephemeralPool is set, the root-disk pool is a tmpfs pool that
// reconcileRootDisk creates on demand, so it won't (and shouldn't) exist yet.
func (r *LibvirtMachineReconciler) preflightPools(ctx context.Context, rc *reconcileCtx) error {
	log := logf.FromContext(ctx)
	lm := rc.libvirtMachine

	var required []requiredPool
	seen := map[string]bool{}
	add := func(name, label, reason string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		required = append(required, requiredPool{name: name, label: label, reason: reason})
	}

	// Pool the cluster stages its base qcow2 into (LibvirtCluster.spec.baseImage).
	if rc.libvirtCluster != nil && rc.libvirtCluster.Spec.BaseImage != nil {
		add(rc.libvirtCluster.Spec.BaseImage.Pool, "base image", infrav1.ReasonBaseImagePoolNotFound)
	}
	// Pool the root disk is cloned/backed from (rootDisk.baseImagePool, which
	// defaults to storagePool).
	add(rc.baseImagePool, "base image", infrav1.ReasonBaseImagePoolNotFound)
	// Pool the root disk is created in — skipped when CAPLV provisions an
	// ephemeral tmpfs pool, which doesn't exist until reconcileRootDisk runs.
	if !lm.Spec.RootDisk.EphemeralPool {
		add(rc.storagePool, "root disk", infrav1.ReasonStoragePoolNotFound)
	}
	// Pools for any additional data disks.
	for _, d := range lm.Spec.AdditionalDisks {
		add(d.StoragePool, "additional disk", infrav1.ReasonStoragePoolNotFound)
	}

	for _, p := range required {
		exists, err := rc.libvirtClient.PoolExists(ctx, p.name)
		if err != nil {
			return r.handleLibvirtError(lm, err, "checking "+p.label+" pool")
		}
		if exists {
			continue
		}
		msg := fmt.Sprintf(
			"Storage pool %q (%s) does not exist on host %q; create it on the host or point the spec at an existing pool",
			p.name, p.label, rc.libvirtHost.Name)
		log.Info("Terminal error: storage pool not found",
			"pool", p.name, "use", p.label, "host", rc.libvirtHost.Name, "reason", p.reason)
		r.setTerminalError(lm, p.reason, msg)
		apimeta.SetStatusCondition(&lm.Status.Conditions, metav1.Condition{
			Type:               infrav1.InfrastructureReadyCondition,
			Status:             metav1.ConditionFalse,
			Reason:             p.reason,
			Message:            msg,
			ObservedGeneration: lm.Generation,
		})
		return nil
	}
	return nil
}

// reconcileBaseImage stages the cluster-level base qcow2 onto this host if
// LibvirtCluster.spec.baseImage is set and the configured volume isn't
// already present in the pool. The qcow2 is fetched into the controller's
// local cache (singleflight'd across machines in the same cluster) and
// streamed onto the host via SSH, then registered with libvirt via
// vol-create-as + vol-upload. Multiple machines on the same host racing
// to stage the same image are coalesced per (host, sha256).
func (r *LibvirtMachineReconciler) reconcileBaseImage(ctx context.Context, rc *reconcileCtx) error {
	log := logf.FromContext(ctx)
	if rc.libvirtCluster == nil || rc.libvirtCluster.Spec.BaseImage == nil {
		return nil
	}
	if r.BaseImageCache == nil {
		return fmt.Errorf("baseImage requested but no image cache is wired")
	}
	bi := rc.libvirtCluster.Spec.BaseImage

	// Fast path: volume already present in the pool — nothing to do.
	// The pool itself is verified up front by preflightPools, so a failure
	// here is a genuine libvirt error rather than a missing-pool config issue.
	exists, err := rc.libvirtClient.VolumeExists(ctx, bi.Pool, bi.VolumeName)
	if err != nil {
		return r.handleLibvirtError(rc.libvirtMachine, err, "checking base image volume")
	}
	if exists {
		return nil
	}

	apimeta.SetStatusCondition(&rc.libvirtMachine.Status.Conditions, metav1.Condition{
		Type:               infrav1.BaseImageStagedCondition,
		Status:             metav1.ConditionFalse,
		Reason:             infrav1.ReasonBaseImageStaging,
		Message:            fmt.Sprintf("Staging base image to %s on %s", bi.Pool, rc.libvirtHost.Name),
		ObservedGeneration: rc.libvirtMachine.Generation,
	})

	// Resolve credentials, fetch into local cache (singleflight'd).
	creds, err := r.resolveBaseImageCredentials(ctx, rc.libvirtCluster)
	if err != nil {
		return fmt.Errorf("resolving baseImage credentials: %w", err)
	}
	entry, err := r.BaseImageCache.Get(ctx, bi.Source, creds, baseImageExpectedSHA256(bi.Source))
	if err != nil {
		return fmt.Errorf("fetching baseImage: %w", err)
	}

	// Per-(host, sha) singleflight: prevent N machines on the same host from
	// all uploading the same qcow2 in parallel.
	uploadKey := rc.libvirtHost.Name + "|" + entry.SHA256
	uploaderAny, _ := r.baseImageHostUpload.LoadOrStore(uploadKey, &sync.Mutex{})
	uploader := uploaderAny.(*sync.Mutex)
	uploader.Lock()
	defer uploader.Unlock()

	// Re-check existence under the lock: another reconcile may have just
	// completed the upload while we waited.
	if exists, err := rc.libvirtClient.VolumeExists(ctx, bi.Pool, bi.VolumeName); err != nil {
		return r.handleLibvirtError(rc.libvirtMachine, err, "rechecking base image volume")
	} else if exists {
		return nil
	}

	virtualSize, err := libvirt.Qcow2VirtualSize(entry.Path)
	if err != nil {
		return fmt.Errorf("read qcow2 header: %w", err)
	}

	f, err := os.Open(entry.Path)
	if err != nil {
		return fmt.Errorf("open cached qcow2: %w", err)
	}
	defer func() { _ = f.Close() }()

	log.Info("Staging base image", "pool", bi.Pool, "volume", bi.VolumeName,
		"sha256", entry.SHA256, "virtualSize", virtualSize, "host", rc.libvirtHost.Name)
	if err := rc.libvirtClient.UploadQcow2Volume(ctx, bi.Pool, bi.VolumeName, f, virtualSize); err != nil {
		return r.handleLibvirtError(rc.libvirtMachine, err, "uploading base image")
	}

	apimeta.SetStatusCondition(&rc.libvirtMachine.Status.Conditions, metav1.Condition{
		Type:               infrav1.BaseImageStagedCondition,
		Status:             metav1.ConditionTrue,
		Reason:             infrav1.ReasonBaseImageStaged,
		Message:            fmt.Sprintf("Base image staged in %s as %s", bi.Pool, bi.VolumeName),
		ObservedGeneration: rc.libvirtMachine.Generation,
	})
	return nil
}

// baseImageExpectedSHA256 returns the user-supplied digest from the active
// transport, or "" when none was provided.
func baseImageExpectedSHA256(src infrav1.BaseImageSource) string {
	switch src.Type {
	case infrav1.BootArtifactsSourceHTTPS:
		if src.HTTPS != nil {
			return src.HTTPS.SHA256
		}
	case infrav1.BootArtifactsSourceOCI:
		if src.OCI != nil {
			return src.OCI.SHA256
		}
	case infrav1.BootArtifactsSourceS3:
		if src.S3 != nil {
			return src.S3.SHA256
		}
	}
	return ""
}

// resolveBaseImageCredentials reads the Secret referenced by the active
// transport's CredentialsSecretRef. Returns (nil, nil) when no ref is set.
func (r *LibvirtMachineReconciler) resolveBaseImageCredentials(
	ctx context.Context, libvirtCluster *infrav1.LibvirtCluster,
) (*imagecache.Credentials, error) {
	bi := libvirtCluster.Spec.BaseImage
	if bi == nil {
		return nil, nil
	}
	var ref *infrav1.BootArtifactsSecretReference
	switch bi.Source.Type {
	case infrav1.BootArtifactsSourceHTTPS:
		if bi.Source.HTTPS != nil {
			ref = bi.Source.HTTPS.CredentialsSecretRef
		}
	case infrav1.BootArtifactsSourceOCI:
		if bi.Source.OCI != nil {
			ref = bi.Source.OCI.CredentialsSecretRef
		}
	case infrav1.BootArtifactsSourceS3:
		if bi.Source.S3 != nil {
			ref = bi.Source.S3.CredentialsSecretRef
		}
	}
	if ref == nil {
		return nil, nil
	}

	ns, err := resolveSecretNamespace(ref.Namespace, libvirtCluster.Namespace,
		"spec.baseImage.source.*.credentialsSecretRef.namespace")
	if err != nil {
		return nil, err
	}
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: ns, Name: ref.Name}, secret); err != nil {
		return nil, fmt.Errorf("get secret %s/%s: %w", ns, ref.Name, err)
	}

	switch bi.Source.Type {
	case infrav1.BootArtifactsSourceOCI:
		creds, err := ociCredentialsFromSecret(secret, bi.Source.OCI.Reference)
		if err != nil {
			return nil, err
		}
		return &imagecache.Credentials{Username: creds.Username, Password: creds.Password}, nil
	case infrav1.BootArtifactsSourceS3:
		creds, err := s3CredentialsFromSecret(secret)
		if err != nil {
			return nil, err
		}
		return &imagecache.Credentials{
			AccessKeyID:     creds.AccessKeyID,
			SecretAccessKey: creds.SecretAccessKey,
			SessionToken:    creds.SessionToken,
		}, nil
	case infrav1.BootArtifactsSourceHTTPS:
		// HTTPS uses username/password from the same shapes as OCI basic auth.
		creds, err := ociCredentialsFromSecret(secret, "")
		if err != nil {
			return nil, err
		}
		return &imagecache.Credentials{Username: creds.Username, Password: creds.Password}, nil
	}
	return nil, nil
}

// reconcileRootDisk ensures the ephemeral pool (if requested) and root disk
// volume exist on the libvirt host.
func (r *LibvirtMachineReconciler) reconcileRootDisk(ctx context.Context, rc *reconcileCtx) error {
	log := logf.FromContext(ctx)
	libvirtMachine := rc.libvirtMachine
	libvirtClient := rc.libvirtClient

	// Create ephemeral tmpfs pool if requested and not yet present.
	if libvirtMachine.Spec.RootDisk.EphemeralPool {
		ephPoolName := rc.machineScope.EphemeralPoolName()
		ephPoolPath := rc.machineScope.EphemeralPoolPath()
		poolExists, err := libvirtClient.PoolExists(ctx, ephPoolName)
		if err != nil {
			return r.handleLibvirtError(libvirtMachine, err, "checking ephemeral pool")
		}
		if poolExists {
			// A libvirt daemon restart leaves the pool defined but inactive,
			// and every volume operation against it fails with "pool is not
			// active". Start it; if that fails the tmpfs backing is gone, so
			// destroy the stale definition and recreate from scratch.
			active, err := libvirtClient.PoolIsActive(ctx, ephPoolName)
			if err != nil {
				return r.handleLibvirtError(libvirtMachine, err, "checking ephemeral pool state")
			}
			if !active {
				log.Info("Ephemeral pool is inactive, starting it", "pool", ephPoolName)
				if err := libvirtClient.StartPool(ctx, ephPoolName); err != nil {
					log.Info("Failed to start inactive ephemeral pool, destroying and recreating",
						"pool", ephPoolName, "reason", err.Error())
					if err := libvirtClient.DestroyPool(ctx, ephPoolName); err != nil {
						return r.handleLibvirtError(libvirtMachine, err, "destroying stale ephemeral pool")
					}
					poolExists = false
				}
			}
		}
		if !poolExists {
			ephPoolSize := libvirtMachine.Spec.RootDisk.EphemeralPoolSize
			log.Info("Creating ephemeral tmpfs pool", "pool", ephPoolName, "path", ephPoolPath, "size", ephPoolSize)
			if err := libvirtClient.CreateTmpfsPool(ctx, ephPoolName, ephPoolPath, ephPoolSize); err != nil {
				log.Error(err, "Failed to create ephemeral tmpfs pool", "pool", ephPoolName)
				return r.handleLibvirtError(libvirtMachine, err, "creating ephemeral pool")
			}
			log.Info("Created ephemeral tmpfs pool", "pool", ephPoolName)
		}
		// Override storagePool to use the ephemeral pool for all VM artifacts.
		rc.storagePool = ephPoolName
	}

	// Create root disk if it does not exist.
	rootExists, err := libvirtClient.VolumeExists(ctx, rc.storagePool, rc.rootDiskVolume)
	if err != nil {
		return r.handleLibvirtError(libvirtMachine, err, "checking root disk")
	}
	if !rootExists {
		log.Info("Creating root disk volume", "volume", rc.rootDiskVolume, "strategy", libvirtMachine.Spec.RootDisk.CloneStrategy)
		sizeBytes := libvirtMachine.Spec.RootDisk.Size.Value()
		diskStart := time.Now()

		switch libvirtMachine.Spec.RootDisk.CloneStrategy {
		case infrav1.CloneStrategyFullClone:
			if err := libvirtClient.CloneVolume(ctx, rc.baseImagePool, libvirtMachine.Spec.RootDisk.BaseImage, rc.rootDiskVolume); err != nil {
				if libvirt.IsNotFound(err) {
					log.Error(err, "Terminal error", "operation", "cloning root disk (full-clone)", "reason", infrav1.ReasonBaseImageNotFound)
					r.setTerminalError(libvirtMachine, infrav1.ReasonBaseImageNotFound,
						fmt.Sprintf("Base image %q not found in pool %q", libvirtMachine.Spec.RootDisk.BaseImage, rc.baseImagePool))
					return nil
				}
				log.Error(err, "Failed to clone root disk (full-clone)", "volume", rc.rootDiskVolume)
				return r.handleLibvirtError(libvirtMachine, err, "cloning root disk (full-clone)")
			}
		default: // copy-on-write
			backingPath, err := libvirtClient.GetVolumePath(ctx, rc.baseImagePool, libvirtMachine.Spec.RootDisk.BaseImage)
			if err != nil {
				if libvirt.IsNotFound(err) {
					log.Error(err, "Terminal error", "operation", "getting base image path", "reason", infrav1.ReasonBaseImageNotFound)
					r.setTerminalError(libvirtMachine, infrav1.ReasonBaseImageNotFound,
						fmt.Sprintf("Base image %q not found in pool %q", libvirtMachine.Spec.RootDisk.BaseImage, rc.baseImagePool))
					return nil
				}
				log.Error(err, "Failed to get base image path", "volume", rc.rootDiskVolume)
				return r.handleLibvirtError(libvirtMachine, err, "getting base image path")
			}
			if err := libvirtClient.CreateVolumeFromBackingStore(ctx, rc.storagePool, rc.rootDiskVolume, backingPath, sizeBytes); err != nil {
				log.Error(err, "Failed to create root disk (copy-on-write)", "volume", rc.rootDiskVolume)
				return r.handleLibvirtError(libvirtMachine, err, "creating root disk (copy-on-write)")
			}
		}
		log.Info("Created root disk volume", "volume", rc.rootDiskVolume, "duration", time.Since(diskStart).String())
	}
	return nil
}

// reconcileBootstrapArtifacts prepares bootstrap data (ignition or cloud-init)
// and writes it to the libvirt host.
func (r *LibvirtMachineReconciler) reconcileBootstrapArtifacts(ctx context.Context, rc *reconcileCtx) error {
	log := logf.FromContext(ctx)
	libvirtMachine := rc.libvirtMachine
	libvirtClient := rc.libvirtClient

	bootstrapData, err := rc.machineScope.GetBootstrapData(ctx)
	if err != nil {
		return fmt.Errorf("failed to get bootstrap data: %w", err)
	}

	switch libvirtMachine.Spec.BootstrapFormat {
	case infrav1.BootstrapFormatIgnition:
		hostname := rc.machineScope.DomainName()
		providerID := rc.machineScope.ProviderID()
		injected, err := ignition.InjectMachineMetadata(bootstrapData, hostname, providerID)
		if err != nil {
			log.Info("Could not inject machine metadata into ignition (using original)", "error", err)
		} else {
			bootstrapData = injected
			log.Info("Injected machine metadata into ignition config", "hostname", hostname, "providerID", providerID)
		}

		// Inject static network configuration for OVN-Kubernetes br-ex.
		netSpec := libvirtMachine.Spec.Network
		if len(netSpec.Addresses) > 0 {
			netCfg := ignition.NetworkConfig{
				Addresses: netSpec.Addresses,
				Gateway:   netSpec.Gateway,
			}
			if netSpec.DNS != nil {
				netCfg.DNSServers = netSpec.DNS.Nameservers
				netCfg.DNSSearch = netSpec.DNS.SearchDomains
			}
			injectedNet, err := ignition.InjectStaticNetwork(bootstrapData, netCfg)
			if err != nil {
				log.Info("Could not inject static network config into ignition (using original)", "error", err)
			} else {
				bootstrapData = injectedNet
				log.Info("Injected static network config into ignition", "addresses", netSpec.Addresses, "gateway", netSpec.Gateway)
			}
		}

		// Write the full ignition config to the host filesystem.
		log.Info("Writing ignition config to host", "path", rc.ignitionFilePath)
		bootstrapStart := time.Now()
		if err := libvirtClient.WriteRemoteFile(ctx, rc.ignitionFilePath, bootstrapData); err != nil {
			log.Error(err, "Failed to write ignition file", "path", rc.ignitionFilePath)
			return r.handleLibvirtError(libvirtMachine, err, "writing ignition file")
		}
		log.Info("Wrote ignition config", "path", rc.ignitionFilePath, "size", len(bootstrapData), "duration", time.Since(bootstrapStart).String())

	case infrav1.BootstrapFormatCloudInit:
		isoExists, err := libvirtClient.VolumeExists(ctx, rc.storagePool, rc.bootstrapISO)
		if err != nil {
			return r.handleLibvirtError(libvirtMachine, err, "checking bootstrap ISO")
		}
		if !isoExists {
			log.Info("Creating cloud-init ISO", "iso", rc.bootstrapISO)
			bootstrapStart := time.Now()
			isoData, err := r.ISOBuilder.BuildCloudInitISO(bootstrapData, rc.domainName, rc.domainName)
			if err != nil {
				log.Error(err, "Terminal error", "operation", "building cloud-init ISO", "reason", infrav1.ReasonInvalidBootstrapData)
				r.setTerminalError(libvirtMachine, infrav1.ReasonInvalidBootstrapData,
					fmt.Sprintf("failed to build cloud-init ISO: %v", err))
				return nil
			}
			if err := libvirtClient.UploadVolumeFromBytes(ctx, rc.storagePool, rc.bootstrapISO, isoData); err != nil {
				log.Error(err, "Failed to upload cloud-init ISO", "iso", rc.bootstrapISO)
				return r.handleLibvirtError(libvirtMachine, err, "uploading cloud-init ISO")
			}
			log.Info("Created cloud-init ISO", "iso", rc.bootstrapISO, "duration", time.Since(bootstrapStart).String())
		}

	default:
		log.Error(fmt.Errorf("unsupported bootstrap format: %s", libvirtMachine.Spec.BootstrapFormat),
			"Terminal error", "operation", "preparing bootstrap", "reason", infrav1.ReasonInvalidBootstrapData)
		r.setTerminalError(libvirtMachine, infrav1.ReasonInvalidBootstrapData,
			fmt.Sprintf("unsupported bootstrap format: %s", libvirtMachine.Spec.BootstrapFormat))
	}
	return nil
}

// reconcileAdditionalDisks ensures all additional disks exist and populates
// the reconcileCtx with volume names and disk parameters.
func (r *LibvirtMachineReconciler) reconcileAdditionalDisks(ctx context.Context, rc *reconcileCtx) error {
	log := logf.FromContext(ctx)
	libvirtMachine := rc.libvirtMachine
	libvirtClient := rc.libvirtClient

	for _, disk := range libvirtMachine.Spec.AdditionalDisks {
		volName := fmt.Sprintf("%s-%s.qcow2", rc.machineScope.ArtifactBaseName(), disk.Name)
		rc.additionalDiskVolumes = append(rc.additionalDiskVolumes, volName)

		exists, err := libvirtClient.VolumeExists(ctx, disk.StoragePool, volName)
		if err != nil {
			return r.handleLibvirtError(libvirtMachine, err, "checking additional disk")
		}
		if !exists {
			log.Info("Creating additional disk", "volume", volName)
			if err := libvirtClient.CreateVolume(ctx, disk.StoragePool, volName, disk.Size.Value()); err != nil {
				return r.handleLibvirtError(libvirtMachine, err, "creating additional disk")
			}
		}

		diskPath, err := libvirtClient.GetVolumePath(ctx, disk.StoragePool, volName)
		if err != nil {
			return r.handleLibvirtError(libvirtMachine, err, "getting additional disk path")
		}
		rc.additionalDiskParams = append(rc.additionalDiskParams, libvirt.DiskParam{
			Path: diskPath,
			Bus:  disk.Bus,
		})
	}
	return nil
}

// reconcileBootArtifacts fetches kernel+initramfs and stages them on the
// libvirt host when LibvirtCluster.Spec.BootArtifacts is set. Cached bytes
// are reused across machines via r.artifactCache, and host-side files are
// content-addressed under <HostPath>/<sha256>/.
func (r *LibvirtMachineReconciler) reconcileBootArtifacts(ctx context.Context, rc *reconcileCtx) error {
	log := logf.FromContext(ctx)
	if rc.libvirtCluster == nil || rc.libvirtCluster.Spec.BootArtifacts == nil {
		return nil
	}
	if r.BootArtifactsResolver == nil {
		return fmt.Errorf("bootArtifacts requested but no resolver is wired")
	}
	ba := rc.libvirtCluster.Spec.BootArtifacts

	creds, err := r.resolveBootArtifactsCredentials(ctx, rc.libvirtCluster, ba.Source)
	if err != nil {
		return fmt.Errorf("resolving boot artifacts credentials: %w", err)
	}

	artifacts, err := r.resolveCachedArtifacts(ctx, ba.Source, creds)
	if err != nil {
		return fmt.Errorf("resolving boot artifacts: %w", err)
	}

	hostPath := strings.TrimRight(ba.HostPath, "/")
	kernelPath := path.Join(hostPath, artifacts.KernelSHA256, "vmlinuz")
	initrdPath := path.Join(hostPath, artifacts.InitramfsSHA256, "initramfs.img")

	if err := r.ensureRemoteArtifact(ctx, rc.libvirtClient, kernelPath, artifacts.KernelBytes); err != nil {
		return r.handleLibvirtError(rc.libvirtMachine, err, "staging boot kernel")
	}
	if err := r.ensureRemoteArtifact(ctx, rc.libvirtClient, initrdPath, artifacts.InitramfsBytes); err != nil {
		return r.handleLibvirtError(rc.libvirtMachine, err, "staging boot initramfs")
	}

	rc.kernelPath = kernelPath
	rc.initrdPath = initrdPath
	rc.kernelCmdline = ba.KernelArgs
	log.Info("Direct-boot artifacts staged", "kernel", kernelPath, "initrd", initrdPath)
	return nil
}

func (r *LibvirtMachineReconciler) resolveCachedArtifacts(
	ctx context.Context,
	src infrav1.BootArtifactsSource,
	creds *bootartifacts.Credentials,
) (*bootartifacts.Artifacts, error) {
	key := bootArtifactsCacheKey(src)
	if cached, ok := r.artifactCache.Load(key); ok {
		return cached.(*bootartifacts.Artifacts), nil
	}
	artifacts, err := r.BootArtifactsResolver.Resolve(ctx, src, creds)
	if err != nil {
		return nil, err
	}
	r.artifactCache.Store(key, artifacts)
	return artifacts, nil
}

// resolveBootArtifactsCredentials reads the optional Secret referenced by the
// source spec and returns the credentials to hand to the Resolver. Returns
// (nil, nil) when no secret is referenced.
func (r *LibvirtMachineReconciler) resolveBootArtifactsCredentials(
	ctx context.Context,
	libvirtCluster *infrav1.LibvirtCluster,
	src infrav1.BootArtifactsSource,
) (*bootartifacts.Credentials, error) {
	var ref *infrav1.BootArtifactsSecretReference
	switch src.Type {
	case infrav1.BootArtifactsSourceOCI:
		if src.OCI != nil {
			ref = src.OCI.CredentialsSecretRef
		}
	case infrav1.BootArtifactsSourceS3:
		if src.S3 != nil {
			ref = src.S3.CredentialsSecretRef
		}
	}
	if ref == nil {
		return nil, nil
	}

	ns, err := resolveSecretNamespace(ref.Namespace, libvirtCluster.Namespace,
		"spec.bootArtifacts.source.*.credentialsSecretRef.namespace")
	if err != nil {
		return nil, err
	}
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: ns, Name: ref.Name}, secret); err != nil {
		return nil, fmt.Errorf("get secret %s/%s: %w", ns, ref.Name, err)
	}

	switch src.Type {
	case infrav1.BootArtifactsSourceOCI:
		return ociCredentialsFromSecret(secret, src.OCI.Reference)
	case infrav1.BootArtifactsSourceS3:
		return s3CredentialsFromSecret(secret)
	default:
		return nil, fmt.Errorf("credentials not supported for source type %q", src.Type)
	}
}

// ociCredentialsFromSecret prefers a kubernetes.io/dockerconfigjson Secret
// and falls back to plain username/password keys.
func ociCredentialsFromSecret(secret *corev1.Secret, reference string) (*bootartifacts.Credentials, error) {
	if raw, ok := secret.Data[corev1.DockerConfigJsonKey]; ok && len(raw) > 0 {
		user, pass, err := lookupDockerConfigAuth(raw, reference)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", corev1.DockerConfigJsonKey, err)
		}
		return &bootartifacts.Credentials{Username: user, Password: pass}, nil
	}
	user := string(secret.Data["username"])
	pass := string(secret.Data["password"])
	if user == "" && pass == "" {
		return nil, fmt.Errorf("secret has neither %s nor username/password keys", corev1.DockerConfigJsonKey)
	}
	return &bootartifacts.Credentials{Username: user, Password: pass}, nil
}

func s3CredentialsFromSecret(secret *corev1.Secret) (*bootartifacts.Credentials, error) {
	keyID := firstNonEmptyString(secret.Data["accessKeyID"], secret.Data["AWS_ACCESS_KEY_ID"])
	secretKey := firstNonEmptyString(secret.Data["secretAccessKey"], secret.Data["AWS_SECRET_ACCESS_KEY"])
	sessionToken := firstNonEmptyString(secret.Data["sessionToken"], secret.Data["AWS_SESSION_TOKEN"])
	if keyID == "" || secretKey == "" {
		return nil, fmt.Errorf("secret missing accessKeyID and/or secretAccessKey")
	}
	return &bootartifacts.Credentials{
		AccessKeyID:     keyID,
		SecretAccessKey: secretKey,
		SessionToken:    sessionToken,
	}, nil
}

func firstNonEmptyString(candidates ...[]byte) string {
	for _, c := range candidates {
		if len(c) > 0 {
			return string(c)
		}
	}
	return ""
}

// lookupDockerConfigAuth parses a dockerconfigjson blob and returns the
// username/password for the host of the supplied reference, falling back to
// any registered entry if no host match is found.
func lookupDockerConfigAuth(raw []byte, reference string) (string, string, error) {
	var cfg struct {
		Auths map[string]struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Auth     string `json:"auth"`
		} `json:"auths"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return "", "", err
	}
	host := registryHostFromReference(reference)
	if entry, ok := cfg.Auths[host]; ok {
		return decodeDockerAuth(entry.Username, entry.Password, entry.Auth)
	}
	for h, entry := range cfg.Auths {
		if strings.Contains(h, host) || strings.Contains(host, h) {
			return decodeDockerAuth(entry.Username, entry.Password, entry.Auth)
		}
	}
	for _, entry := range cfg.Auths {
		return decodeDockerAuth(entry.Username, entry.Password, entry.Auth)
	}
	return "", "", fmt.Errorf("no auth entries in dockerconfigjson")
}

func decodeDockerAuth(user, pass, encoded string) (string, string, error) {
	if user != "" || pass != "" {
		return user, pass, nil
	}
	if encoded == "" {
		return "", "", fmt.Errorf("empty auth entry")
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", "", fmt.Errorf("decode auth: %w", err)
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("malformed auth entry")
	}
	return parts[0], parts[1], nil
}

func registryHostFromReference(ref string) string {
	if i := strings.Index(ref, "/"); i > 0 {
		return ref[:i]
	}
	return ref
}

func (r *LibvirtMachineReconciler) ensureRemoteArtifact(
	ctx context.Context,
	c libvirt.Client,
	remotePath string,
	data []byte,
) error {
	exists, err := c.RemoteFileExists(ctx, remotePath)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return c.WriteRemoteFile(ctx, remotePath, data)
}

// bootArtifactsCacheKey produces a stable cache key from a source spec so we
// don't re-fetch the same artifacts for every machine in a cluster.
func bootArtifactsCacheKey(src infrav1.BootArtifactsSource) string {
	var b strings.Builder
	b.WriteString("type=")
	b.WriteString(string(src.Type))
	b.WriteByte('\n')
	if src.HTTPS != nil {
		b.WriteString("https.kernel=" + src.HTTPS.KernelURL + "\n")
		b.WriteString("https.initramfs=" + src.HTTPS.InitramfsURL + "\n")
		b.WriteString("https.kernel.sha256=" + src.HTTPS.KernelSHA256 + "\n")
		b.WriteString("https.initramfs.sha256=" + src.HTTPS.InitramfsSHA256 + "\n")
	}
	if src.OCI != nil {
		b.WriteString("oci.ref=" + src.OCI.Reference + "\n")
		b.WriteString("oci.kernel.title=" + src.OCI.KernelLayerTitle + "\n")
		b.WriteString("oci.initramfs.title=" + src.OCI.InitramfsLayerTitle + "\n")
		b.WriteString("oci.kernel.sha256=" + src.OCI.KernelSHA256 + "\n")
		b.WriteString("oci.initramfs.sha256=" + src.OCI.InitramfsSHA256 + "\n")
	}
	if src.S3 != nil {
		b.WriteString("s3.endpoint=" + src.S3.Endpoint + "\n")
		b.WriteString("s3.region=" + src.S3.Region + "\n")
		b.WriteString("s3.bucket=" + src.S3.Bucket + "\n")
		b.WriteString("s3.kernel=" + src.S3.KernelKey + "\n")
		b.WriteString("s3.initramfs=" + src.S3.InitramfsKey + "\n")
		b.WriteString("s3.kernel.sha256=" + src.S3.KernelSHA256 + "\n")
		b.WriteString("s3.initramfs.sha256=" + src.S3.InitramfsSHA256 + "\n")
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// reconcileDomain defines and starts the libvirt domain, returning its info.
func (r *LibvirtMachineReconciler) reconcileDomain(ctx context.Context, rc *reconcileCtx) (*libvirt.DomainInfo, error) {
	log := logf.FromContext(ctx)
	libvirtMachine := rc.libvirtMachine
	libvirtClient := rc.libvirtClient

	domainExists, err := libvirtClient.DomainExists(ctx, rc.domainName)
	if err != nil {
		return nil, r.handleLibvirtError(libvirtMachine, err, "checking domain")
	}
	if !domainExists {
		log.Info("Defining domain", "domain", rc.domainName)
		defineStart := time.Now()
		rootDiskPath, err := libvirtClient.GetVolumePath(ctx, rc.storagePool, rc.rootDiskVolume)
		if err != nil {
			return nil, r.handleLibvirtError(libvirtMachine, err, "getting root disk path")
		}

		xmlParams := libvirt.DomainXMLParams{
			Name:            rc.domainName,
			VCPUs:           rc.resolvedVCPUs,
			MemoryKB:        int64(rc.resolvedMemoryMB) * memoryMBToKBMultiplier,
			Machine:         libvirtMachine.Spec.Domain.Machine,
			Firmware:        string(libvirtMachine.Spec.Domain.Firmware),
			FirmwarePath:    rc.libvirtHost.Spec.FirmwarePath,
			NVRAMPath:       rc.nvramPath,
			NVRAMTemplate:   rc.libvirtHost.Spec.NVRAMTemplatePath,
			RootDiskPath:    rootDiskPath,
			RootDiskBus:     libvirtMachine.Spec.RootDisk.Bus,
			AdditionalDisks: rc.additionalDiskParams,
			NetworkType:     string(libvirtMachine.Spec.Network.Type),
			NetworkName:     libvirtMachine.Spec.Network.Name,
			NetworkModel:    libvirtMachine.Spec.Network.Model,
			MACAddress:      libvirtMachine.Spec.Network.MACAddress,
			KernelPath:      rc.kernelPath,
			InitrdPath:      rc.initrdPath,
			KernelCmdline:   rc.kernelCmdline,
		}

		switch libvirtMachine.Spec.BootstrapFormat {
		case infrav1.BootstrapFormatIgnition:
			xmlParams.IgnitionPath = rc.ignitionFilePath
		case infrav1.BootstrapFormatCloudInit:
			isoPath, err := libvirtClient.GetVolumePath(ctx, rc.storagePool, rc.bootstrapISO)
			if err != nil {
				return nil, r.handleLibvirtError(libvirtMachine, err, "getting cloud-init ISO path")
			}
			xmlParams.BootstrapISOPath = isoPath
		}

		xmlDef, err := libvirt.GenerateDomainXML(xmlParams)
		if err != nil {
			log.Error(err, "Terminal error", "operation", "generating domain XML", "reason", infrav1.ReasonSpecMismatch)
			r.setTerminalError(libvirtMachine, infrav1.ReasonSpecMismatch,
				fmt.Sprintf("failed to generate domain XML: %v", err))
			return nil, nil
		}

		if _, err := libvirtClient.DefineDomain(ctx, xmlDef); err != nil {
			log.Error(err, "Failed to define domain", "domain", rc.domainName)
			return nil, r.handleLibvirtError(libvirtMachine, err, "defining domain")
		}
		log.Info("Defined domain", "domain", rc.domainName, "duration", time.Since(defineStart).String())
	}

	// Start domain if not running.
	domainInfo, err := libvirtClient.GetDomain(ctx, rc.domainName)
	if err != nil {
		return nil, r.handleLibvirtError(libvirtMachine, err, "getting domain info")
	}
	if domainInfo.State != "running" {
		log.Info("Starting domain", "domain", rc.domainName, "currentState", domainInfo.State)
		startTime := time.Now()
		if err := libvirtClient.StartDomain(ctx, rc.domainName); err != nil {
			log.Error(err, "Failed to start domain", "domain", rc.domainName)
			return nil, r.handleLibvirtError(libvirtMachine, err, "starting domain")
		}
		log.Info("Started domain", "domain", rc.domainName, "duration", time.Since(startTime).String())
		// Re-fetch domain info after start.
		domainInfo, err = libvirtClient.GetDomain(ctx, rc.domainName)
		if err != nil {
			return nil, r.handleLibvirtError(libvirtMachine, err, "getting domain info after start")
		}
	}
	return domainInfo, nil
}

// reconcileDelete cleans up libvirt artifacts and removes the finalizer.
func (r *LibvirtMachineReconciler) reconcileDelete(
	ctx context.Context,
	libvirtMachine *infrav1.LibvirtMachine,
	_ *clusterv1.Machine,
	cluster *clusterv1.Cluster,
	libvirtCluster *infrav1.LibvirtCluster,
	libvirtHost *infrav1.LibvirtHost,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// If finalizer is not present, nothing to do.
	if !controllerutil.ContainsFinalizer(libvirtMachine, infrav1.MachineFinalizer) {
		return ctrl.Result{}, nil
	}

	// Check host reachability.
	if !libvirtHost.Status.Ready {
		log.Info("LibvirtHost is not reachable, stalling cleanup", "host", libvirtHost.Name)
		apimeta.SetStatusCondition(&libvirtMachine.Status.Conditions, metav1.Condition{
			Type:               infrav1.CleanupStalledCondition,
			Status:             metav1.ConditionTrue,
			Reason:             infrav1.ReasonCleanupStalled,
			Message:            "Cannot clean up: LibvirtHost is not reachable",
			ObservedGeneration: libvirtMachine.Generation,
		})
		return ctrl.Result{RequeueAfter: cleanupStalledRequeueInterval}, nil
	}

	// Create clients.
	sshClient, libvirtClient, err := r.createClients(ctx, libvirtHost)
	if err != nil {
		apimeta.SetStatusCondition(&libvirtMachine.Status.Conditions, metav1.Condition{
			Type:               infrav1.CleanupStalledCondition,
			Status:             metav1.ConditionTrue,
			Reason:             infrav1.ReasonCleanupStalled,
			Message:            "Cannot clean up: " + err.Error(),
			ObservedGeneration: libvirtMachine.Generation,
		})
		return ctrl.Result{RequeueAfter: cleanupStalledRequeueInterval}, nil
	}
	defer func() { _ = sshClient.Close() }()
	defer func() { _ = libvirtClient.Close() }()

	// Build scope for artifact names.
	machineScope, err := scope.NewMachineScope(scope.MachineScopeParams{
		Client:         r.Client,
		Cluster:        cluster,
		Machine:        &clusterv1.Machine{}, // Minimal; only used for artifact name computation.
		LibvirtCluster: libvirtCluster,
		LibvirtMachine: libvirtMachine,
		LibvirtHost:    libvirtHost,
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create machine scope for cleanup: %w", err)
	}

	domainName := machineScope.DomainName()
	rootDiskVolume := machineScope.RootDiskVolumeName()
	bootstrapISO := machineScope.BootstrapISOName()
	storagePool := libvirtMachine.Spec.RootDisk.StoragePool
	// If ephemeral pool was used, volumes live in the per-machine pool.
	if libvirtMachine.Spec.RootDisk.EphemeralPool {
		storagePool = machineScope.EphemeralPoolName()
	}

	// Enrich logger with host and domain context.
	log = log.WithValues("host", libvirtHost.Name, "domain", domainName)

	// Destroy domain (ignore not-found).
	log.Info("Destroying domain", "domain", domainName)
	if err := libvirtClient.DestroyDomain(ctx, domainName); err != nil && !libvirt.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("failed to destroy domain: %w", err)
	}

	// Undefine domain (ignore not-found).
	log.Info("Undefining domain", "domain", domainName)
	if err := libvirtClient.UndefineDomain(ctx, domainName); err != nil && !libvirt.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("failed to undefine domain: %w", err)
	}

	// Delete root disk volume.
	log.Info("Deleting root disk volume", "volume", rootDiskVolume)
	if err := libvirtClient.DeleteVolume(ctx, storagePool, rootDiskVolume); err != nil && !libvirt.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("failed to delete root disk: %w", err)
	}

	// Delete bootstrap artifacts.
	switch libvirtMachine.Spec.BootstrapFormat {
	case infrav1.BootstrapFormatIgnition:
		ignitionFile := machineScope.IgnitionFilePath()
		log.Info("Deleting ignition file", "path", ignitionFile)
		if err := libvirtClient.DeleteRemoteFile(ctx, ignitionFile); err != nil {
			log.Error(err, "Failed to delete ignition file (non-fatal)", "path", ignitionFile)
		}
	case infrav1.BootstrapFormatCloudInit:
		log.Info("Deleting cloud-init ISO", "iso", bootstrapISO)
		if err := libvirtClient.DeleteVolume(ctx, storagePool, bootstrapISO); err != nil && !libvirt.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to delete cloud-init ISO: %w", err)
		}
	}

	// Delete additional disks.
	for _, disk := range libvirtMachine.Spec.AdditionalDisks {
		volName := fmt.Sprintf("%s-%s.qcow2", machineScope.ArtifactBaseName(), disk.Name)
		log.Info("Deleting additional disk", "volume", volName)
		if err := libvirtClient.DeleteVolume(ctx, disk.StoragePool, volName); err != nil && !libvirt.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to delete additional disk %s: %w", volName, err)
		}
	}

	// Destroy ephemeral tmpfs pool if managed by CAPLV.
	if libvirtMachine.Spec.RootDisk.EphemeralPool {
		ephPoolName := machineScope.EphemeralPoolName()
		log.Info("Destroying ephemeral pool and tmpfs", "pool", ephPoolName)
		if err := libvirtClient.DestroyPool(ctx, ephPoolName); err != nil && !libvirt.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to destroy ephemeral pool: %w", err)
		}
	}

	// Delete the Node object from the cluster if it exists.
	nodeName := machineScope.DomainName()
	node := &corev1.Node{}
	if err := r.Get(ctx, client.ObjectKey{Name: nodeName}, node); err == nil {
		log.Info("Deleting Node object", "node", nodeName)
		if err := r.Delete(ctx, node); err != nil && !apierrors.IsNotFound(err) {
			log.Error(err, "Failed to delete Node object (non-fatal)", "node", nodeName)
		}
	}

	// Remove finalizer.
	controllerutil.RemoveFinalizer(libvirtMachine, infrav1.MachineFinalizer)

	return ctrl.Result{}, nil
}

// createClients creates SSH and libvirt clients for the given host.
func (r *LibvirtMachineReconciler) createClients(ctx context.Context, host *infrav1.LibvirtHost) (*gossh.Client, libvirt.Client, error) {
	if host.Spec.SecretRef == nil {
		return nil, nil, fmt.Errorf("LibvirtHost %s has no secretRef configured", host.Name)
	}

	secretNS, err := resolveSecretNamespace(host.Spec.SecretRef.Namespace, host.Namespace, "spec.secretRef.namespace")
	if err != nil {
		return nil, nil, err
	}
	secret := &corev1.Secret{}
	secretKey := types.NamespacedName{Namespace: secretNS, Name: host.Spec.SecretRef.Name}
	if err := r.Get(ctx, secretKey, secret); err != nil {
		return nil, nil, fmt.Errorf("failed to get SSH secret: %w", err)
	}

	connURI, err := libvirt.LocalConnectionURI(host.Spec.URI)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid libvirt connection URI: %w", err)
	}

	sshClient, err := r.SSHClientFactory(ctx, host, secret)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create SSH client: %w", err)
	}

	libvirtClient := r.LibvirtClientFactory(sshClient, connURI)
	return sshClient, libvirtClient, nil
}

// handleLibvirtError inspects the error and returns an appropriate error.
// Terminal libvirt errors are recorded on the machine status and nil is returned
// (stopping reconciliation); transient errors are returned for retry.
func (r *LibvirtMachineReconciler) handleLibvirtError(
	libvirtMachine *infrav1.LibvirtMachine,
	err error,
	operation string,
) error {
	if libvirt.IsTerminal(err) {
		reason := infrav1.ReasonSpecMismatch
		msg := fmt.Sprintf("terminal error during %s: %v", operation, err)
		libvirtMachine.Status.FailureReason = &reason
		libvirtMachine.Status.FailureMessage = &msg
		libvirtMachine.Status.Ready = false
		return nil
	}
	return fmt.Errorf("error during %s: %w", operation, err)
}

// setTerminalError sets a terminal failure on the machine and stops reconciliation.
func (r *LibvirtMachineReconciler) setTerminalError(
	libvirtMachine *infrav1.LibvirtMachine,
	reason string,
	message string,
) {
	libvirtMachine.Status.FailureReason = &reason
	libvirtMachine.Status.FailureMessage = &message
	libvirtMachine.Status.Ready = false
}

// SetupWithManager sets up the controller with the Manager.
func (r *LibvirtMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	concurrency := r.MaxConcurrentReconciles
	if concurrency <= 0 {
		concurrency = defaultMaxConcurrentReconciles
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.LibvirtMachine{}).
		Watches(&clusterv1.Machine{}, handler.EnqueueRequestForOwner(
			mgr.GetScheme(), mgr.GetRESTMapper(), &infrav1.LibvirtMachine{},
		)).
		WithOptions(controller.Options{MaxConcurrentReconciles: concurrency}).
		Named("libvirtmachine").
		Complete(r)
}
