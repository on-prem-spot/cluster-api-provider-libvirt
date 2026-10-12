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
	"fmt"
	"time"

	gossh "golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
	"github.com/on-prem-spot/cluster-api-provider-libvirt/internal/libvirt"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/cluster-api/util/predicates"
)

const (
	// hostActiveRequeueInterval is used when machines reference this host.
	hostActiveRequeueInterval = 5 * time.Minute
	defaultReservedVCPUs      = 2
	defaultReservedMemoryMB   = 4096
	kilobytesPerMegabyte      = 1024
)

// SSHClientFactory is a function that creates an SSH client from a LibvirtHost and Secret.
type SSHClientFactory func(ctx context.Context, host *infrav1.LibvirtHost, secret *corev1.Secret) (*gossh.Client, error)

// LibvirtClientFactory is a function that creates a libvirt Client from an SSH
// client and the local connection URI virsh should use on the host
// (libvirt.ConnURISystem or libvirt.ConnURISession).
type LibvirtClientFactory func(sshClient *gossh.Client, connURI string) libvirt.Client

// LibvirtHostReconciler reconciles a LibvirtHost object.
type LibvirtHostReconciler struct {
	client.Client
	Scheme               *runtime.Scheme
	SSHClientFactory     SSHClientFactory
	LibvirtClientFactory LibvirtClientFactory
	Recorder             record.EventRecorder
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirthosts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirthosts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirthosts/finalizers,verbs=update
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirtmachines,verbs=list
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile performs a connectivity check against the libvirt host and updates status.
// Health checks only requeue when machines actively reference this host.
func (r *LibvirtHostReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("host", req.Name, "namespace", req.Namespace)
	log.Info("Reconciling LibvirtHost")
	ctx = logf.IntoContext(ctx, log)

	// Fetch the LibvirtHost resource.
	host := &infrav1.LibvirtHost{}
	if err := r.Get(ctx, req.NamespacedName, host); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if _, externallyManaged := host.Annotations[clusterv1.ManagedByAnnotation]; externallyManaged {
		return ctrl.Result{}, nil
	}

	// Create patch helper for deferred status patch.
	patchHelper, err := patch.NewHelper(host, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}
	defer func() {
		if patchErr := patchHelper.Patch(ctx, host); patchErr != nil {
			log.Error(patchErr, "Failed to patch LibvirtHost")
		}
	}()

	// Always update LastChecked.
	now := metav1.Now()
	host.Status.LastChecked = &now

	// Run the health check (SSH + libvirt + capacity discovery).
	r.performHealthCheck(ctx, host)

	// Only requeue for ongoing monitoring if machines reference this host.
	hasActiveMachines, err := r.hasReferencingMachines(ctx, host)
	if err != nil {
		log.Error(err, "Failed to check for referencing machines")
		// On error, requeue to retry the check.
		return ctrl.Result{RequeueAfter: healthCheckIntervalForHost(host)}, nil
	}

	if hasActiveMachines {
		interval := healthCheckIntervalForHost(host)
		log.V(1).Info("Host has active machines, scheduling next health check", "interval", interval)
		return ctrl.Result{RequeueAfter: interval}, nil
	}

	// No active machines — don't requeue. The next reconcile will be
	// triggered by a spec change or when a LibvirtMachine referencing
	// this host is created.
	log.V(1).Info("No active machines reference this host, not requeueing")
	return ctrl.Result{}, nil
}

// performHealthCheck verifies SSH + libvirt connectivity and discovers capacity.
func (r *LibvirtHostReconciler) performHealthCheck(ctx context.Context, host *infrav1.LibvirtHost) {
	log := logf.FromContext(ctx)

	// secretRef is required.
	if host.Spec.SecretRef == nil {
		r.markUnreachable(host, infrav1.ReasonConnectionFailed, "spec.secretRef is required for SSH connectivity")
		return
	}

	// Fetch the SSH secret. The reference is confined to this host's own
	// namespace; see resolveSecretNamespace.
	secretNS, err := resolveSecretNamespace(host.Spec.SecretRef.Namespace, host.Namespace, "spec.secretRef.namespace")
	if err != nil {
		log.Error(err, "Rejected Secret reference")
		r.markUnreachable(host, infrav1.ReasonConnectionFailed, err.Error())
		return
	}
	secret := &corev1.Secret{}
	secretKey := types.NamespacedName{Namespace: secretNS, Name: host.Spec.SecretRef.Name}
	if err := r.Get(ctx, secretKey, secret); err != nil {
		log.Error(err, "Failed to get SSH secret", "secret", secretKey)
		r.markUnreachable(host, infrav1.ReasonConnectionFailed, "Failed to get SSH secret: "+err.Error())
		return
	}

	// Create SSH client.
	sshClient, err := r.SSHClientFactory(ctx, host, secret)
	if err != nil {
		log.Error(err, "Failed to create SSH client")
		r.markUnreachable(host, infrav1.ReasonConnectionFailed, "SSH connection failed: "+err.Error())
		return
	}
	if sshClient != nil {
		defer func() { _ = sshClient.Close() }()
	}

	// Verify libvirt is usable.
	connURI, err := libvirt.LocalConnectionURI(host.Spec.URI)
	if err != nil {
		log.Error(err, "Invalid libvirt connection URI")
		r.markUnreachable(host, infrav1.ReasonConnectionFailed, "Invalid libvirt connection URI: "+err.Error())
		return
	}
	libvirtClient := r.LibvirtClientFactory(sshClient, connURI)
	defer func() { _ = libvirtClient.Close() }()

	if err := libvirtClient.Ping(ctx); err != nil {
		log.Error(err, "Libvirt connectivity check failed")
		r.markUnreachable(host, infrav1.ReasonConnectionFailed, "Libvirt connectivity check failed: "+err.Error())
		return
	}

	// Connectivity proves libvirt-client + daemon are present, but not that the
	// KVM hypervisor stack is installed. Verify the host can actually run the
	// kvm domains CAPLV defines so a partial install surfaces here rather than
	// at first machine provision.
	if err := libvirtClient.VerifyHypervisor(ctx); err != nil {
		log.Error(err, "Hypervisor capability check failed")
		r.markUnreachable(host, infrav1.ReasonHypervisorUnavailable, "Libvirt is reachable but the QEMU/KVM hypervisor is unavailable (is qemu-kvm / libvirt-daemon-driver-qemu installed and KVM enabled?): "+err.Error())
		return
	}

	// Session-mode hosts additionally need setup no virsh query covers — a
	// setuid qemu-bridge-helper for unprivileged bridge attachment and
	// loginctl lingering so the session daemon (and its VMs) survives the
	// controller's SSH sessions closing. Catch a partially set-up host here
	// rather than at first machine provision (or worse, first disconnect).
	if connURI == libvirt.ConnURISession {
		if err := libvirtClient.VerifySessionPrerequisites(ctx); err != nil {
			log.Error(err, "Session-mode prerequisites check failed")
			r.markUnreachable(host, infrav1.ReasonSessionModeMisconfigured, "Host is not set up for session-mode libvirt (run setup-host.yaml with libvirt_mode=session): "+err.Error())
			return
		}
	}

	// Discover host capacity.
	nodeInfo, err := libvirtClient.GetNodeInfo(ctx)
	if err != nil {
		log.Error(err, "Failed to get node info")
		r.markUnreachable(host, infrav1.ReasonConnectionFailed, "Failed to get host capacity: "+err.Error())
		return
	}

	// Compute available resources after reservations.
	reservedVCPUs := int32(defaultReservedVCPUs)
	reservedMemoryMB := int32(defaultReservedMemoryMB)
	if host.Spec.ReservedResources != nil {
		reservedVCPUs = host.Spec.ReservedResources.VCPUs
		reservedMemoryMB = host.Spec.ReservedResources.MemoryMB
	}
	totalMemoryMB := int32(nodeInfo.MemoryKB / kilobytesPerMegabyte)
	availVCPUs := nodeInfo.CPUs - reservedVCPUs
	availMemoryMB := totalMemoryMB - reservedMemoryMB
	if availVCPUs < 0 {
		availVCPUs = 0
	}
	if availMemoryMB < 0 {
		availMemoryMB = 0
	}

	host.Status.Capacity = &infrav1.HostCapacity{
		TotalVCPUs:        nodeInfo.CPUs,
		TotalMemoryMB:     totalMemoryMB,
		AvailableVCPUs:    availVCPUs,
		AvailableMemoryMB: availMemoryMB,
	}

	// SSH + libvirt verified, capacity discovered.
	log.Info("Host health check passed", "totalVCPUs", nodeInfo.CPUs, "availableVCPUs", availVCPUs, "availableMemoryMB", availMemoryMB)
	r.markReachable(host, fmt.Sprintf("SSH and libvirt verified; %d vCPUs / %d MB available", availVCPUs, availMemoryMB))
}

// markUnreachable records a failed health check on the host.
func (r *LibvirtHostReconciler) markUnreachable(host *infrav1.LibvirtHost, reason, message string) {
	host.Status.Ready = false
	r.setReachable(host, metav1.ConditionFalse, reason, message)
}

// markReachable records a passed health check on the host.
func (r *LibvirtHostReconciler) markReachable(host *infrav1.LibvirtHost, message string) {
	host.Status.Ready = true
	r.setReachable(host, metav1.ConditionTrue, infrav1.ReasonConnectionSucceeded, message)
}

// setReachable updates the Reachable condition and emits an event, but only
// when the status or reason actually changes. Hosts are health-checked on a
// timer for as long as machines reference them, so emitting unconditionally
// would produce an event per reconcile for a host that is simply still down.
func (r *LibvirtHostReconciler) setReachable(
	host *infrav1.LibvirtHost,
	status metav1.ConditionStatus,
	reason string,
	message string,
) {
	// Copy the previous status and reason rather than holding the pointer
	// FindStatusCondition returns: it aliases the slice element that
	// SetStatusCondition is about to overwrite in place, so comparing through
	// it afterwards would always report no change and never emit.
	var prevStatus metav1.ConditionStatus
	var prevReason string
	if previous := apimeta.FindStatusCondition(host.Status.Conditions, infrav1.HostReachableCondition); previous != nil {
		prevStatus, prevReason = previous.Status, previous.Reason
	}
	apimeta.SetStatusCondition(&host.Status.Conditions, metav1.Condition{
		Type:               infrav1.HostReachableCondition,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: host.Generation,
	})
	if prevStatus == status && prevReason == reason {
		return
	}
	eventType := corev1.EventTypeWarning
	if status == metav1.ConditionTrue {
		eventType = corev1.EventTypeNormal
	}
	recordEvent(r.Recorder, host, eventType, reason, message)
}

// hasReferencingMachines returns true if any LibvirtMachine in the same
// namespace references this host via spec.hostRef.
func (r *LibvirtHostReconciler) hasReferencingMachines(ctx context.Context, host *infrav1.LibvirtHost) (bool, error) {
	machineList := &infrav1.LibvirtMachineList{}
	if err := r.List(ctx, machineList, client.InNamespace(host.Namespace)); err != nil {
		return false, err
	}
	for i := range machineList.Items {
		if machineList.Items[i].Spec.HostRef.Name == host.Name {
			return true, nil
		}
	}
	return false, nil
}

func healthCheckIntervalForHost(host *infrav1.LibvirtHost) time.Duration {
	if host.Spec.HealthCheckIntervalSeconds > 0 {
		return time.Duration(host.Spec.HealthCheckIntervalSeconds) * time.Second
	}
	return hostActiveRequeueInterval
}

// SetupWithManager sets up the controller with the Manager.
func (r *LibvirtHostReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.LibvirtHost{}, builder.WithPredicates(predicates.ResourceIsNotExternallyManaged(mgr.GetScheme(), mgr.GetLogger()))).
		// Re-reconcile the host when a LibvirtMachine referencing it is created or deleted.
		// This starts/stops health check monitoring based on active machines.
		Watches(&infrav1.LibvirtMachine{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, obj client.Object) []ctrl.Request {
				machine, ok := obj.(*infrav1.LibvirtMachine)
				if !ok {
					return nil
				}
				return []ctrl.Request{{
					NamespacedName: types.NamespacedName{
						Name:      machine.Spec.HostRef.Name,
						Namespace: machine.Namespace,
					},
				}}
			},
		)).
		Named("libvirthost").
		Complete(r)
}
