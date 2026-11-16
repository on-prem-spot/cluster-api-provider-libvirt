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
	"strings"
	"testing"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

var ecpKey = types.NamespacedName{Namespace: "workers", Name: "ocp"}

// ecpFixture returns a Cluster named ocp, owning a LibvirtExternalControlPlane
// of the same name, with or without a control plane endpoint.
func ecpFixture(withEndpoint bool) (*clusterv1.Cluster, *infrav1.LibvirtExternalControlPlane) {
	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Namespace: ecpKey.Namespace, Name: ecpKey.Name, UID: "cluster-uid"},
		Spec: clusterv1.ClusterSpec{ControlPlaneRef: clusterv1.ContractVersionedObjectReference{
			APIGroup: infrav1.GroupVersion.Group, Kind: "LibvirtExternalControlPlane", Name: ecpKey.Name,
		}},
	}
	if withEndpoint {
		cluster.Spec.ControlPlaneEndpoint = clusterv1.APIEndpoint{Host: "api.ocp.example.com", Port: 6443}
	}
	cp := &infrav1.LibvirtExternalControlPlane{ObjectMeta: metav1.ObjectMeta{
		Namespace: ecpKey.Namespace, Name: ecpKey.Name, UID: "cp-uid",
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion: clusterv1.GroupVersion.String(), Kind: "Cluster", Name: cluster.Name, UID: cluster.UID,
		}},
	}}
	return cluster, cp
}

func ecpReconciler(t *testing.T, objs ...client.Object) *LibvirtExternalControlPlaneReconciler {
	t.Helper()
	s := testScheme(t)
	if err := rbacv1.AddToScheme(s); err != nil {
		t.Fatalf("add rbacv1 scheme: %v", err)
	}
	if err := authenticationv1.AddToScheme(s); err != nil {
		t.Fatalf("add authenticationv1 scheme: %v", err)
	}
	return &LibvirtExternalControlPlaneReconciler{
		Client: fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).
			WithStatusSubresource(&infrav1.LibvirtExternalControlPlane{}).Build(),
		Scheme:        s,
		APIServer:     "https://172.30.0.1:443",
		CAData:        []byte("ca-bundle"),
		ServerVersion: func() (string, error) { return "v1.35.8", nil },
	}
}

// reconcileECP reconciles until the controller stops asking to requeue at
// once (the first pass only adds the finalizer).
func reconcileECP(t *testing.T, r *LibvirtExternalControlPlaneReconciler) ctrl.Result {
	t.Helper()
	for range 3 {
		result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: ecpKey})
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if !result.Requeue { //nolint:staticcheck // the controller requeues once after adding its finalizer
			return result
		}
	}
	t.Fatal("Reconcile kept asking to requeue")
	return ctrl.Result{}
}

func getECP(t *testing.T, r *LibvirtExternalControlPlaneReconciler) *infrav1.LibvirtExternalControlPlane {
	t.Helper()
	cp := &infrav1.LibvirtExternalControlPlane{}
	if err := r.Get(context.Background(), ecpKey, cp); err != nil {
		t.Fatalf("get LibvirtExternalControlPlane: %v", err)
	}
	return cp
}

func conditionReason(cp *infrav1.LibvirtExternalControlPlane, conditionType string) string {
	if c := apimeta.FindStatusCondition(cp.Status.Conditions, conditionType); c != nil {
		return string(c.Status) + "/" + c.Reason
	}
	return ""
}

func TestExternalControlPlane_WaitsForItsCluster(t *testing.T) {
	_, cp := ecpFixture(true)
	cp.OwnerReferences = nil
	r := ecpReconciler(t, cp)

	reconcileECP(t, r)

	got := getECP(t, r)
	if reason := conditionReason(got, infrav1.AvailableCondition); reason != "False/"+infrav1.ReasonWaitingForCluster {
		t.Errorf("Available = %s, want False/%s", reason, infrav1.ReasonWaitingForCluster)
	}
	if got.Status.Initialization.ControlPlaneInitialized != nil {
		t.Error("expected the control plane not to be reported initialized")
	}
}

func TestExternalControlPlane_WaitsForTheEndpoint(t *testing.T) {
	cluster, cp := ecpFixture(false)
	r := ecpReconciler(t, cluster, cp)

	reconcileECP(t, r)

	got := getECP(t, r)
	if reason := conditionReason(got, infrav1.AvailableCondition); reason != "False/"+infrav1.ReasonWaitingForControlPlaneEndpoint {
		t.Errorf("Available = %s, want False/%s", reason, infrav1.ReasonWaitingForControlPlaneEndpoint)
	}
}

func TestExternalControlPlane_ReportsTheControlPlaneAndWritesItsKubeconfig(t *testing.T) {
	cluster, cp := ecpFixture(true)
	r := ecpReconciler(t, cluster, cp)
	ctx := context.Background()

	result := reconcileECP(t, r)

	got := getECP(t, r)
	if !ptr.Deref(got.Status.Initialization.ControlPlaneInitialized, false) {
		t.Error("expected status.initialization.controlPlaneInitialized")
	}
	if !ptr.Deref(got.Status.ExternalManagedControlPlane, false) {
		t.Error("expected status.externalManagedControlPlane")
	}
	if got.Status.Version != "v1.35.8" {
		t.Errorf("version = %q, want v1.35.8", got.Status.Version)
	}
	for _, conditionType := range []string{infrav1.AvailableCondition, infrav1.KubeconfigReadyCondition} {
		if c := apimeta.FindStatusCondition(got.Status.Conditions, conditionType); c == nil ||
			c.Status != metav1.ConditionTrue || c.Message != "" {
			t.Errorf("%s = %+v, want True with no message", conditionType, c)
		}
	}
	if result.RequeueAfter <= 0 {
		t.Error("expected a requeue to refresh the token")
	}

	// The Secret has the contract's shape and is the control plane's.
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: ecpKey.Namespace, Name: "ocp-kubeconfig"}, secret); err != nil {
		t.Fatalf("get kubeconfig Secret: %v", err)
	}
	if secret.Type != clusterv1.ClusterSecretType || secret.Labels[clusterv1.ClusterNameLabel] != "ocp" {
		t.Errorf("Secret type %q, labels %v; want %s and cluster-name ocp", secret.Type, secret.Labels,
			clusterv1.ClusterSecretType)
	}
	if !metav1.IsControlledBy(secret, got) {
		t.Error("expected the Secret to be controlled by the LibvirtExternalControlPlane")
	}
	config, err := clientcmd.Load(secret.Data["value"])
	if err != nil {
		t.Fatalf("parse kubeconfig: %v", err)
	}
	current := config.Contexts[config.CurrentContext]
	if server := config.Clusters[current.Cluster].Server; server != "https://172.30.0.1:443" {
		t.Errorf("kubeconfig server = %q", server)
	}
	if string(config.Clusters[current.Cluster].CertificateAuthorityData) != "ca-bundle" {
		t.Error("expected the kubeconfig to carry the API server CA")
	}
	if token := config.AuthInfos[current.AuthInfo].Token; token != "fake-token" {
		t.Errorf("kubeconfig token = %q, want the ServiceAccount token", token)
	}

	// The token is the ServiceAccount's, bound to the workload access role.
	binding := &rbacv1.ClusterRoleBinding{}
	if err := r.Get(ctx, client.ObjectKey{Name: clusterRoleBindingName(got)}, binding); err != nil {
		t.Fatalf("get ClusterRoleBinding: %v", err)
	}
	if binding.RoleRef.Name != workloadAccessClusterRole || len(binding.Subjects) != 1 ||
		binding.Subjects[0].Namespace != ecpKey.Namespace || binding.Subjects[0].Name != "ocp-capi-workload" {
		t.Errorf("ClusterRoleBinding roleRef %v, subjects %v", binding.RoleRef, binding.Subjects)
	}
}

// A <cluster>-kubeconfig Secret CAPLV did not create is the user's.
func TestExternalControlPlane_LeavesAUserProvidedKubeconfigAlone(t *testing.T) {
	cluster, cp := ecpFixture(true)
	userSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ecpKey.Namespace, Name: "ocp-kubeconfig"},
		Data:       map[string][]byte{"value": []byte("user kubeconfig")},
	}
	r := ecpReconciler(t, cluster, cp, userSecret)

	reconcileECP(t, r)

	secret := &corev1.Secret{}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(userSecret), secret); err != nil {
		t.Fatalf("get Secret: %v", err)
	}
	if string(secret.Data["value"]) != "user kubeconfig" || len(secret.OwnerReferences) != 0 {
		t.Error("expected the user's Secret to be left as it was")
	}
	got := getECP(t, r)
	if reason := conditionReason(got, infrav1.KubeconfigReadyCondition); reason != "True/"+infrav1.ReasonUserProvidedKubeconfig {
		t.Errorf("KubeconfigReady = %s, want True/%s", reason, infrav1.ReasonUserProvidedKubeconfig)
	}
	if !ptr.Deref(got.Status.Initialization.ControlPlaneInitialized, false) {
		t.Error("expected the control plane to be reported initialized")
	}
}

func TestExternalControlPlane_RefreshesTheTokenAtHalfLife(t *testing.T) {
	cluster, cp := ecpFixture(true)
	r := ecpReconciler(t, cluster, cp)
	reconcileECP(t, r)
	ctx := context.Background()
	secretKey := client.ObjectKey{Namespace: ecpKey.Namespace, Name: "ocp-kubeconfig"}

	setExpiry := func(expires time.Time) {
		secret := &corev1.Secret{}
		if err := r.Get(ctx, secretKey, secret); err != nil {
			t.Fatalf("get Secret: %v", err)
		}
		secret.Annotations[tokenExpiresAnnotation] = expires.UTC().Format(time.RFC3339)
		secret.Data["value"] = []byte("old")
		if err := r.Update(ctx, secret); err != nil {
			t.Fatalf("update Secret: %v", err)
		}
	}
	value := func() string {
		secret := &corev1.Secret{}
		if err := r.Get(ctx, secretKey, secret); err != nil {
			t.Fatalf("get Secret: %v", err)
		}
		return string(secret.Data["value"])
	}
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return now }

	// More than half the lifetime left: kept, and revisited at half-life.
	setExpiry(now.Add(20 * time.Hour))
	result := reconcileECP(t, r)
	if value() != "old" {
		t.Error("expected a token with 20h left to be kept")
	}
	if want := 8 * time.Hour; result.RequeueAfter != want {
		t.Errorf("RequeueAfter = %v, want %v", result.RequeueAfter, want)
	}

	// Less than half left: replaced.
	setExpiry(now.Add(11 * time.Hour))
	reconcileECP(t, r)
	if !strings.Contains(value(), "fake-token") {
		t.Error("expected a token with 11h left to be replaced")
	}
}

func TestExternalControlPlane_DeletionRemovesTheClusterRoleBinding(t *testing.T) {
	cluster, cp := ecpFixture(true)
	r := ecpReconciler(t, cluster, cp)
	reconcileECP(t, r)
	ctx := context.Background()

	if err := r.Delete(ctx, getECP(t, r)); err != nil {
		t.Fatalf("delete LibvirtExternalControlPlane: %v", err)
	}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: ecpKey}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	err := r.Get(ctx, client.ObjectKey{Name: clusterRoleBindingName(cp)}, &rbacv1.ClusterRoleBinding{})
	if !apierrors.IsNotFound(err) {
		t.Errorf("expected the ClusterRoleBinding to be deleted, got %v", err)
	}
	if err := r.Get(ctx, ecpKey, &infrav1.LibvirtExternalControlPlane{}); !apierrors.IsNotFound(err) {
		t.Errorf("expected the finalizer to be removed and the object gone, got %v", err)
	}
}

func TestClusterToControlPlane(t *testing.T) {
	cluster, _ := ecpFixture(true)
	if got := clusterToControlPlane(context.Background(), cluster); len(got) != 1 || got[0].NamespacedName != ecpKey {
		t.Errorf("clusterToControlPlane() = %v, want %v", got, ecpKey)
	}
	cluster.Spec.ControlPlaneRef.Kind = "KubeadmControlPlane"
	cluster.Spec.ControlPlaneRef.APIGroup = "controlplane.cluster.x-k8s.io"
	if got := clusterToControlPlane(context.Background(), cluster); len(got) != 0 {
		t.Errorf("clusterToControlPlane() = %v for a KubeadmControlPlane, want none", got)
	}
}

func TestClusterRoleBindingName(t *testing.T) {
	short := &infrav1.LibvirtExternalControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: "a", Name: "b"}}
	long := &infrav1.LibvirtExternalControlPlane{ObjectMeta: metav1.ObjectMeta{
		Namespace: strings.Repeat("n", 63), Name: strings.Repeat("c", 253)}}
	other := &infrav1.LibvirtExternalControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: "a-b", Name: "c"}}
	if clusterRoleBindingName(short) == clusterRoleBindingName(other) {
		t.Error("expected different control planes to get different names")
	}
	if n := len(clusterRoleBindingName(long)); n > 253 {
		t.Errorf("name is %d characters, want at most 253", n)
	}
}
