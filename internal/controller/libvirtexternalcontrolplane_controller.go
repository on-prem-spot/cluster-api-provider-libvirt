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
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/cluster-api/util/paused"
	"sigs.k8s.io/cluster-api/util/predicates"
)

const (
	// workloadAccessClusterRole is what Cluster API may do in the cluster
	// through <cluster>-kubeconfig: watch, label, drain and delete Nodes. It
	// is config/rbac/capi_workload_access_role.yaml, named with kustomize's
	// caplv- prefix.
	workloadAccessClusterRole = "caplv-capi-workload-access"

	// kubeconfigTokenTTL is how long the token in <cluster>-kubeconfig lasts.
	// It is refreshed once half of that has passed, so Cluster API always
	// holds a token with hours left, and a leaked one soon expires.
	kubeconfigTokenTTL = 24 * time.Hour

	// tokenExpiresAnnotation records on <cluster>-kubeconfig when its token
	// expires, in RFC 3339.
	tokenExpiresAnnotation = "infrastructure.cluster.x-k8s.io/token-expires"

	// controlPlaneNamespaceLabel and controlPlaneNameLabel identify the
	// LibvirtExternalControlPlane behind a ClusterRoleBinding, which, being
	// cluster-scoped, cannot have it as an owner.
	controlPlaneNamespaceLabel = "infrastructure.cluster.x-k8s.io/external-control-plane-namespace"
	controlPlaneNameLabel      = "infrastructure.cluster.x-k8s.io/external-control-plane-name"
)

// LibvirtExternalControlPlaneReconciler reconciles a LibvirtExternalControlPlane
// object: it reports the cluster CAPLV runs on as the Cluster's initialized,
// available control plane, and maintains the <cluster>-kubeconfig Secret
// Cluster API reaches that cluster with.
type LibvirtExternalControlPlaneReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
	// WatchFilterValue, when non-empty, restricts reconciliation to objects
	// labelled cluster.x-k8s.io/watch-filter=<value>.
	WatchFilterValue string

	// APIServer and CAData are the address and CA bundle of the API server
	// as the manager reaches it, written into <cluster>-kubeconfig. Cluster
	// API runs in the same cluster, so it reaches the server the same way.
	APIServer string
	CAData    []byte
	// ServerVersion reports the cluster's Kubernetes version, if set.
	ServerVersion func() (string, error)

	// now returns the current time; tests replace it.
	now func() time.Time
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirtexternalcontrolplanes,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirtexternalcontrolplanes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirtexternalcontrolplanes/finalizers,verbs=update
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters;clusters/status,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=serviceaccounts/token,verbs=create
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterrolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,verbs=bind,resourceNames=caplv-capi-workload-access
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile reports the control plane and maintains its kubeconfig Secret.
func (r *LibvirtExternalControlPlaneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reterr error) {
	log := logf.FromContext(ctx).WithValues("controlPlane", req.Name, "namespace", req.Namespace)
	log.Info("Reconciling LibvirtExternalControlPlane")
	ctx = logf.IntoContext(ctx, log)

	controlPlane := &infrav1.LibvirtExternalControlPlane{}
	if err := r.Get(ctx, req.NamespacedName, controlPlane); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	patchHelper, err := patch.NewHelper(controlPlane, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}
	defer func() {
		if patchErr := patchHelper.Patch(ctx, controlPlane); patchErr != nil {
			log.Error(patchErr, "Failed to patch LibvirtExternalControlPlane")
			reterr = errors.Join(reterr, patchErr)
		}
	}()

	if !controlPlane.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, r.reconcileDelete(ctx, controlPlane)
	}

	cluster, err := util.GetOwnerCluster(ctx, r.Client, controlPlane.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, err
	}
	if cluster == nil {
		log.Info("Waiting for a Cluster to name the LibvirtExternalControlPlane in spec.controlPlaneRef")
		r.setUnavailable(controlPlane, infrav1.ReasonWaitingForCluster,
			"Waiting for a Cluster to name this control plane in spec.controlPlaneRef")
		return ctrl.Result{}, nil
	}

	if isPaused, _, err := paused.EnsurePausedCondition(ctx, r.Client, cluster, controlPlane); err != nil || isPaused {
		if isPaused {
			log.Info("LibvirtExternalControlPlane or owning Cluster is paused, skipping reconciliation")
		}
		return ctrl.Result{}, err
	}

	if controllerutil.AddFinalizer(controlPlane, infrav1.ExternalControlPlaneFinalizer) {
		// Persist the finalizer before creating the ClusterRoleBinding it
		// guards.
		return ctrl.Result{Requeue: true}, nil
	}
	controlPlane.Status.ExternalManagedControlPlane = ptr.To(true)

	// The endpoint comes from the LibvirtCluster, through the Cluster.
	if !cluster.Spec.ControlPlaneEndpoint.IsValid() {
		r.setUnavailable(controlPlane, infrav1.ReasonWaitingForControlPlaneEndpoint,
			"Waiting for the Cluster's spec.controlPlaneEndpoint, set from its LibvirtCluster")
		return ctrl.Result{}, nil
	}

	refreshIn, err := r.reconcileKubeconfig(ctx, controlPlane, cluster)
	if err != nil {
		apimeta.SetStatusCondition(&controlPlane.Status.Conditions, metav1.Condition{
			Type:               infrav1.KubeconfigReadyCondition,
			Status:             metav1.ConditionFalse,
			Reason:             infrav1.ReasonKubeconfigFailed,
			Message:            err.Error(),
			ObservedGeneration: controlPlane.Generation,
		})
		r.setUnavailable(controlPlane, infrav1.ReasonKubeconfigFailed,
			"Cluster API cannot reach the cluster without <cluster>-kubeconfig")
		return ctrl.Result{}, err
	}

	if r.ServerVersion != nil {
		if version, err := r.ServerVersion(); err != nil {
			log.Error(err, "Failed to read the cluster's Kubernetes version")
		} else {
			controlPlane.Status.Version = version
		}
	}

	if !ptr.Deref(controlPlane.Status.Initialization.ControlPlaneInitialized, false) {
		recordEvent(r.Recorder, controlPlane, corev1.EventTypeNormal, infrav1.ReasonAvailable, ActionProvision,
			"Reported the existing control plane of Cluster %s as initialized", cluster.Name)
	}
	controlPlane.Status.Initialization.ControlPlaneInitialized = ptr.To(true)
	apimeta.SetStatusCondition(&controlPlane.Status.Conditions, metav1.Condition{
		Type:               infrav1.AvailableCondition,
		Status:             metav1.ConditionTrue,
		Reason:             infrav1.ReasonAvailable,
		ObservedGeneration: controlPlane.Generation,
	})
	return ctrl.Result{RequeueAfter: refreshIn}, nil
}

// setUnavailable reports the control plane not available, for reason.
// controlPlaneInitialized, once true, stays true, as the contract requires.
func (r *LibvirtExternalControlPlaneReconciler) setUnavailable(cp *infrav1.LibvirtExternalControlPlane, reason, message string) {
	apimeta.SetStatusCondition(&cp.Status.Conditions, metav1.Condition{
		Type:               infrav1.AvailableCondition,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: cp.Generation,
	})
}

// reconcileKubeconfig makes sure <cluster>-kubeconfig exists and holds a
// token with at least half its lifetime left, and returns how long until it
// must be refreshed. A Secret of that name that CAPLV did not create is the
// user's: it is used as it is and left alone.
//
// The token is the ServiceAccount <cluster>-capi-workload's, bound to the
// workload access ClusterRole and nothing else, so Cluster API can do what
// it needs to the cluster's Nodes but no more.
func (r *LibvirtExternalControlPlaneReconciler) reconcileKubeconfig(
	ctx context.Context,
	cp *infrav1.LibvirtExternalControlPlane,
	cluster *clusterv1.Cluster,
) (time.Duration, error) {
	secret := &corev1.Secret{}
	secretKey := client.ObjectKey{Namespace: cluster.Namespace, Name: cluster.Name + "-kubeconfig"}
	switch err := r.Get(ctx, secretKey, secret); {
	case apierrors.IsNotFound(err):
		secret = nil
	case err != nil:
		return 0, fmt.Errorf("failed to get Secret %s: %w", secretKey.Name, err)
	case !metav1.IsControlledBy(secret, cp):
		apimeta.SetStatusCondition(&cp.Status.Conditions, metav1.Condition{
			Type:               infrav1.KubeconfigReadyCondition,
			Status:             metav1.ConditionTrue,
			Reason:             infrav1.ReasonUserProvidedKubeconfig,
			ObservedGeneration: cp.Generation,
		})
		return 0, nil
	}

	serviceAccount, err := r.ensureServiceAccount(ctx, cp, cluster)
	if err != nil {
		return 0, err
	}
	if err := r.ensureClusterRoleBinding(ctx, cp, serviceAccount); err != nil {
		return 0, err
	}

	now := r.clock()
	if secret != nil {
		if expires, err := time.Parse(time.RFC3339, secret.Annotations[tokenExpiresAnnotation]); err == nil {
			if refreshAt := expires.Add(-kubeconfigTokenTTL / 2); now.Before(refreshAt) {
				r.setKubeconfigReady(cp)
				return refreshAt.Sub(now), nil
			}
		}
	}

	tokenRequest := &authenticationv1.TokenRequest{Spec: authenticationv1.TokenRequestSpec{
		ExpirationSeconds: ptr.To(int64(kubeconfigTokenTTL.Seconds())),
	}}
	if err := r.SubResource("token").Create(ctx, serviceAccount, tokenRequest); err != nil {
		return 0, fmt.Errorf("failed to request a token for ServiceAccount %s: %w", serviceAccount.Name, err)
	}
	kubeconfig, err := r.kubeconfig(cluster.Name, serviceAccount.Name, tokenRequest.Status.Token)
	if err != nil {
		return 0, err
	}
	expires := tokenRequest.Status.ExpirationTimestamp.Time
	if expires.IsZero() {
		expires = now.Add(kubeconfigTokenTTL)
	}

	if secret == nil {
		secret = &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: secretKey.Namespace, Name: secretKey.Name}}
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		// The contract's shape: type, label and the value key.
		secret.Type = clusterv1.ClusterSecretType
		if secret.Labels == nil {
			secret.Labels = map[string]string{}
		}
		secret.Labels[clusterv1.ClusterNameLabel] = cluster.Name
		if secret.Annotations == nil {
			secret.Annotations = map[string]string{}
		}
		secret.Annotations[tokenExpiresAnnotation] = expires.UTC().Format(time.RFC3339)
		secret.Data = map[string][]byte{"value": kubeconfig}
		return controllerutil.SetControllerReference(cp, secret, r.Scheme)
	}); err != nil {
		return 0, fmt.Errorf("failed to write Secret %s: %w", secretKey.Name, err)
	}
	logf.FromContext(ctx).Info("Wrote kubeconfig Secret", "secret", secretKey.Name, "expires", expires)
	r.setKubeconfigReady(cp)
	return expires.Add(-kubeconfigTokenTTL / 2).Sub(now), nil
}

func (r *LibvirtExternalControlPlaneReconciler) setKubeconfigReady(cp *infrav1.LibvirtExternalControlPlane) {
	apimeta.SetStatusCondition(&cp.Status.Conditions, metav1.Condition{
		Type:               infrav1.KubeconfigReadyCondition,
		Status:             metav1.ConditionTrue,
		Reason:             infrav1.ReasonKubeconfigReady,
		ObservedGeneration: cp.Generation,
	})
}

func (r *LibvirtExternalControlPlaneReconciler) ensureServiceAccount(
	ctx context.Context,
	cp *infrav1.LibvirtExternalControlPlane,
	cluster *clusterv1.Cluster,
) (*corev1.ServiceAccount, error) {
	serviceAccount := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
		Namespace: cluster.Namespace,
		Name:      cluster.Name + "-capi-workload",
	}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, serviceAccount, func() error {
		if serviceAccount.Labels == nil {
			serviceAccount.Labels = map[string]string{}
		}
		serviceAccount.Labels[clusterv1.ClusterNameLabel] = cluster.Name
		return controllerutil.SetControllerReference(cp, serviceAccount, r.Scheme)
	}); err != nil {
		return nil, fmt.Errorf("failed to ensure ServiceAccount %s: %w", serviceAccount.Name, err)
	}
	return serviceAccount, nil
}

func (r *LibvirtExternalControlPlaneReconciler) ensureClusterRoleBinding(
	ctx context.Context,
	cp *infrav1.LibvirtExternalControlPlane,
	serviceAccount *corev1.ServiceAccount,
) error {
	binding := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: clusterRoleBindingName(cp)}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, binding, func() error {
		if binding.Labels == nil {
			binding.Labels = map[string]string{}
		}
		binding.Labels[controlPlaneNamespaceLabel] = cp.Namespace
		binding.Labels[controlPlaneNameLabel] = cp.Name
		// roleRef cannot change, and never needs to: it is always this role.
		binding.RoleRef = rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     workloadAccessClusterRole,
		}
		binding.Subjects = []rbacv1.Subject{{
			Kind:      rbacv1.ServiceAccountKind,
			Namespace: serviceAccount.Namespace,
			Name:      serviceAccount.Name,
		}}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to ensure ClusterRoleBinding %s: %w", binding.Name, err)
	}
	return nil
}

// reconcileDelete removes the ClusterRoleBinding; the ServiceAccount and the
// kubeconfig Secret are owned by the control plane and garbage collected.
func (r *LibvirtExternalControlPlaneReconciler) reconcileDelete(ctx context.Context, cp *infrav1.LibvirtExternalControlPlane) error {
	binding := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: clusterRoleBindingName(cp)}}
	if err := r.Delete(ctx, binding); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete ClusterRoleBinding %s: %w", binding.Name, err)
	}
	controllerutil.RemoveFinalizer(cp, infrav1.ExternalControlPlaneFinalizer)
	return nil
}

// clusterRoleBindingName names a control plane's ClusterRoleBinding. Names
// are cluster-wide, so it includes the namespace, and a hash keeps it unique
// and within the 253-character limit however long the names are.
func clusterRoleBindingName(cp *infrav1.LibvirtExternalControlPlane) string {
	sum := sha256.Sum256([]byte(cp.Namespace + "/" + cp.Name))
	name := fmt.Sprintf("caplv-capi-workload-%s-%s", cp.Namespace, cp.Name)
	if len(name) > 200 {
		name = name[:200]
	}
	return name + "-" + hex.EncodeToString(sum[:])[:10]
}

// kubeconfig renders a kubeconfig for the API server with token.
func (r *LibvirtExternalControlPlaneReconciler) kubeconfig(clusterName, user, token string) ([]byte, error) {
	config := clientcmdapi.NewConfig()
	config.Clusters[clusterName] = &clientcmdapi.Cluster{
		Server:                   r.APIServer,
		CertificateAuthorityData: r.CAData,
	}
	config.AuthInfos[user] = &clientcmdapi.AuthInfo{Token: token}
	config.Contexts[clusterName] = &clientcmdapi.Context{Cluster: clusterName, AuthInfo: user}
	config.CurrentContext = clusterName
	data, err := clientcmd.Write(*config)
	if err != nil {
		return nil, fmt.Errorf("failed to render kubeconfig: %w", err)
	}
	return data, nil
}

func (r *LibvirtExternalControlPlaneReconciler) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

// clusterToControlPlane maps a Cluster to the LibvirtExternalControlPlane its
// spec.controlPlaneRef names, if any.
func clusterToControlPlane(_ context.Context, o client.Object) []reconcile.Request {
	cluster, ok := o.(*clusterv1.Cluster)
	if !ok {
		return nil
	}
	ref := cluster.Spec.ControlPlaneRef
	if ref.APIGroup != infrav1.GroupVersion.Group || ref.Kind != "LibvirtExternalControlPlane" || ref.Name == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: client.ObjectKey{Namespace: cluster.Namespace, Name: ref.Name}}}
}

// SetupWithManager sets up the controller with the Manager.
func (r *LibvirtExternalControlPlaneReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.LibvirtExternalControlPlane{}, builder.WithPredicates(
			predicates.ResourceHasFilterLabel(mgr.GetScheme(), mgr.GetLogger(), r.WatchFilterValue),
		)).
		// The endpoint, pausing and ownership come from the Cluster.
		Watches(&clusterv1.Cluster{}, handler.EnqueueRequestsFromMapFunc(clusterToControlPlane),
			builder.WithPredicates(predicates.ResourceHasFilterLabel(mgr.GetScheme(), mgr.GetLogger(), r.WatchFilterValue))).
		// Recreate the kubeconfig Secret if it is deleted.
		Owns(&corev1.Secret{}).
		Named("libvirtexternalcontrolplane").
		Complete(r)
}
