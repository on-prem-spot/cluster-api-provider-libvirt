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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/cluster-api/util/predicates"
)

// LibvirtClusterReconciler reconciles a LibvirtCluster object.
type LibvirtClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirtclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirtclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirtclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters;clusters/status,verbs=get;list;watch

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

	// CAPLV does not provision the endpoint. Requiring the API server to be
	// reachable here would prevent CAPI from provisioning the control plane.
	endpoint := libvirtCluster.Spec.ControlPlaneEndpoint
	ready := endpoint.Host != "" && endpoint.Port > 0 && endpoint.Port <= 65535
	condition := metav1.Condition{
		Type: "Ready", Status: metav1.ConditionFalse,
		Reason:             "WaitingForControlPlaneEndpoint",
		Message:            "Configure a control plane endpoint host and port",
		ObservedGeneration: libvirtCluster.Generation,
	}
	if ready {
		condition.Status = metav1.ConditionTrue
		condition.Reason = infrav1.ReasonClusterReady
		condition.Message = fmt.Sprintf("Control plane endpoint %s:%d is configured", endpoint.Host, endpoint.Port)
	}
	libvirtCluster.Status.Ready = ready
	apimeta.SetStatusCondition(&libvirtCluster.Status.Conditions, condition)
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *LibvirtClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.LibvirtCluster{}, builder.WithPredicates(predicates.ResourceIsNotExternallyManaged(mgr.GetScheme(), mgr.GetLogger()))).
		Watches(&clusterv1.Cluster{}, handler.EnqueueRequestsFromMapFunc(
			util.ClusterToInfrastructureMapFunc(context.Background(), infrav1.GroupVersion.WithKind("LibvirtCluster"), mgr.GetClient(), &infrav1.LibvirtCluster{}),
		)).
		Named("libvirtcluster").
		Complete(r)
}
