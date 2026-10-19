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
	"errors"
	"fmt"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/cluster-api/util/predicates"
)

// LibvirtClusterReconciler reconciles a LibvirtCluster object.
type LibvirtClusterReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
	// WatchFilterValue, when non-empty, restricts reconciliation to objects
	// labelled cluster.x-k8s.io/watch-filter=<value>.
	WatchFilterValue string
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirtclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirtclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirtclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters;clusters/status,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirthosts,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile marks infrastructure ready once its externally managed endpoint is configured.
func (r *LibvirtClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reterr error) {
	log := logf.FromContext(ctx).WithValues("cluster", req.Name, "namespace", req.Namespace)
	log.Info("Reconciling LibvirtCluster")
	ctx = logf.IntoContext(ctx, log)

	// Fetch the LibvirtCluster resource.
	libvirtCluster := &infrav1.LibvirtCluster{}
	if err := r.Get(ctx, req.NamespacedName, libvirtCluster); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if _, externallyManaged := libvirtCluster.Annotations[clusterv1.ManagedByAnnotation]; externallyManaged {
		return ctrl.Result{}, nil
	}

	// Create patch helper for deferred status patch.
	patchHelper, err := patch.NewHelper(libvirtCluster, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}
	defer func() {
		if patchErr := patchHelper.Patch(ctx, libvirtCluster); patchErr != nil {
			log.Error(patchErr, "Failed to patch LibvirtCluster")
			reterr = errors.Join(reterr, patchErr)
		}
	}()

	// Fetch the owner Cluster.
	cluster, err := util.GetOwnerCluster(ctx, r.Client, libvirtCluster.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, err
	}
	if cluster == nil {
		log.Info("Waiting for Cluster controller to set OwnerRef on LibvirtCluster")
		return ctrl.Result{}, nil
	}

	// If cluster is paused, return without requeueing.
	if annotations.IsPaused(cluster, libvirtCluster) {
		log.Info("LibvirtCluster or owning Cluster is paused, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	if !libvirtCluster.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	failureDomains, err := r.failureDomains(ctx, libvirtCluster.Namespace)
	if err != nil {
		return ctrl.Result{}, err
	}
	libvirtCluster.Status.FailureDomains = failureDomains

	// CAPLV does not provision the endpoint. Requiring the API server to be
	// reachable here would prevent CAPI from provisioning the control plane.
	endpoint := libvirtCluster.Spec.ControlPlaneEndpoint
	ready := endpoint.Host != "" && endpoint.Port > 0 && endpoint.Port <= 65535
	condition := metav1.Condition{
		Type: "Ready", Status: metav1.ConditionFalse,
		Reason:             infrav1.ReasonWaitingForControlPlaneEndpoint,
		Message:            "Configure a control plane endpoint host and port",
		ObservedGeneration: libvirtCluster.Generation,
	}
	if ready {
		condition.Status = metav1.ConditionTrue
		condition.Reason = infrav1.ReasonClusterReady
		condition.Message = fmt.Sprintf("Control plane endpoint %s:%d is configured", endpoint.Host, endpoint.Port)
	}
	// Copy, not alias: SetStatusCondition overwrites the element
	// FindStatusCondition points at, so a retained pointer would always
	// compare equal and suppress every event.
	var prevStatus metav1.ConditionStatus
	if previous := apimeta.FindStatusCondition(libvirtCluster.Status.Conditions, "Ready"); previous != nil {
		prevStatus = previous.Status
	}
	libvirtCluster.Status.Ready = ready
	if ready {
		libvirtCluster.Status.Initialization = &infrav1.LibvirtClusterInitializationStatus{Provisioned: ptr.To(true)}
	}
	apimeta.SetStatusCondition(&libvirtCluster.Status.Conditions, condition)
	if prevStatus != condition.Status {
		eventType := corev1.EventTypeNormal
		if !ready {
			eventType = corev1.EventTypeWarning
		}
		recordEvent(r.Recorder, libvirtCluster, eventType, condition.Reason,
			ActionProvision, "%s", condition.Message)
	}
	return ctrl.Result{}, nil
}

// failureDomains reports one failure domain per LibvirtHost in namespace.
// Hosts being deleted are left out so new machines are not steered to them.
// All hosts are control-plane eligible: CAPLV has no notion of a host that
// may only run workers.
func (r *LibvirtClusterReconciler) failureDomains(ctx context.Context, namespace string) ([]infrav1.FailureDomain, error) {
	opts := []client.ListOption{client.InNamespace(namespace)}
	if r.WatchFilterValue != "" {
		opts = append(opts, client.MatchingLabels{clusterv1.WatchLabel: r.WatchFilterValue})
	}
	hosts := &infrav1.LibvirtHostList{}
	if err := r.List(ctx, hosts, opts...); err != nil {
		return nil, fmt.Errorf("failed to list LibvirtHosts: %w", err)
	}
	var domains []infrav1.FailureDomain
	for i := range hosts.Items {
		host := &hosts.Items[i]
		if !host.DeletionTimestamp.IsZero() {
			continue
		}
		domains = append(domains, infrav1.FailureDomain{Name: host.Name, ControlPlane: ptr.To(true)})
	}
	// List order is not guaranteed; a stable order keeps the status patch,
	// and CAPI's copy of it, from changing on every reconcile.
	slices.SortFunc(domains, func(a, b infrav1.FailureDomain) int { return strings.Compare(a.Name, b.Name) })
	return domains, nil
}

// hostToLibvirtClusters maps a LibvirtHost to every LibvirtCluster in its
// namespace, whose failure domains it contributes to.
func (r *LibvirtClusterReconciler) hostToLibvirtClusters(ctx context.Context, obj client.Object) []ctrl.Request {
	clusters := &infrav1.LibvirtClusterList{}
	if err := r.List(ctx, clusters, client.InNamespace(obj.GetNamespace())); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to list LibvirtClusters", "namespace", obj.GetNamespace())
		return nil
	}
	requests := make([]ctrl.Request, 0, len(clusters.Items))
	for i := range clusters.Items {
		requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&clusters.Items[i])})
	}
	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *LibvirtClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.LibvirtCluster{}, builder.WithPredicates(
			predicates.ResourceIsNotExternallyManaged(mgr.GetScheme(), mgr.GetLogger()),
			predicates.ResourceHasFilterLabel(mgr.GetScheme(), mgr.GetLogger(), r.WatchFilterValue),
		)).
		Watches(&clusterv1.Cluster{}, handler.EnqueueRequestsFromMapFunc(
			util.ClusterToInfrastructureMapFunc(context.Background(), infrav1.GroupVersion.WithKind("LibvirtCluster"), mgr.GetClient(), &infrav1.LibvirtCluster{}),
		), builder.WithPredicates(predicates.ResourceHasFilterLabel(mgr.GetScheme(), mgr.GetLogger(), r.WatchFilterValue))).
		// Hosts are failure domains; creation and deletion change the set.
		Watches(&infrav1.LibvirtHost{}, handler.EnqueueRequestsFromMapFunc(r.hostToLibvirtClusters),
			builder.WithPredicates(
				predicates.ResourceHasFilterLabel(mgr.GetScheme(), mgr.GetLogger(), r.WatchFilterValue),
				predicate.Funcs{UpdateFunc: func(e event.UpdateEvent) bool {
					return e.ObjectOld.GetDeletionTimestamp().IsZero() != e.ObjectNew.GetDeletionTimestamp().IsZero()
				}},
			)).
		Named("libvirtcluster").
		Complete(r)
}
