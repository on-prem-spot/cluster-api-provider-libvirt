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
	"testing"

	gossh "golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
	"github.com/on-prem-spot/cluster-api-provider-libvirt/internal/libvirt"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := infrav1.AddToScheme(s); err != nil {
		t.Fatalf("add infrav1 scheme: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("add corev1 scheme: %v", err)
	}
	if err := clusterv1.AddToScheme(s); err != nil {
		t.Fatalf("add clusterv1 scheme: %v", err)
	}
	return s
}

func TestHostReconcile_NoSecretRef_SetsNotReady(t *testing.T) {
	s := testScheme(t)
	host := &infrav1.LibvirtHost{
		ObjectMeta: metav1.ObjectMeta{Name: "host1", Namespace: "default"},
		Spec: infrav1.LibvirtHostSpec{
			URI: "qemu+ssh://root@host/system",
			// SecretRef intentionally nil
		},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(s).WithObjects(host).WithStatusSubresource(host).Build()

	r := &LibvirtHostReconciler{
		Client: k8sClient,
		Scheme: s,
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "host1", Namespace: "default"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated := &infrav1.LibvirtHost{}
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "host1", Namespace: "default"}, updated); err != nil {
		t.Fatalf("get host: %v", err)
	}
	if updated.Status.Ready {
		t.Error("expected Ready=false for host with no secretRef")
	}
	if len(updated.Status.Conditions) == 0 {
		t.Fatal("expected at least one condition")
	}
	cond := updated.Status.Conditions[0]
	if cond.Status != metav1.ConditionFalse {
		t.Errorf("expected condition status False, got %s", cond.Status)
	}
	if cond.Reason != infrav1.ReasonConnectionFailed {
		t.Errorf("expected reason %s, got %s", infrav1.ReasonConnectionFailed, cond.Reason)
	}
}

func TestHostReconcile_SSHFails_SetsNotReady(t *testing.T) {
	s := testScheme(t)
	host := &infrav1.LibvirtHost{
		ObjectMeta: metav1.ObjectMeta{Name: "host1", Namespace: "default"},
		Spec: infrav1.LibvirtHostSpec{
			URI:       "qemu+ssh://root@host/system",
			SecretRef: &infrav1.SecretReference{Name: "ssh-key"},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "ssh-key", Namespace: "default"},
		Data:       map[string][]byte{"ssh-privatekey": []byte("fake-key")},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(s).WithObjects(host, secret).WithStatusSubresource(host).Build()

	r := &LibvirtHostReconciler{
		Client: k8sClient,
		Scheme: s,
		SSHClientFactory: func(_ context.Context, _ *infrav1.LibvirtHost, _ *corev1.Secret) (*gossh.Client, error) {
			return nil, fmt.Errorf("ssh connect failed")
		},
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "host1", Namespace: "default"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated := &infrav1.LibvirtHost{}
	_ = k8sClient.Get(context.Background(), types.NamespacedName{Name: "host1", Namespace: "default"}, updated)
	if updated.Status.Ready {
		t.Error("expected Ready=false when SSH fails")
	}
}

func TestHostReconcile_LibvirtPingFails_SetsNotReady(t *testing.T) {
	s := testScheme(t)
	host := &infrav1.LibvirtHost{
		ObjectMeta: metav1.ObjectMeta{Name: "host1", Namespace: "default"},
		Spec: infrav1.LibvirtHostSpec{
			URI:       "qemu+ssh://root@host/system",
			SecretRef: &infrav1.SecretReference{Name: "ssh-key"},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "ssh-key", Namespace: "default"},
		Data:       map[string][]byte{"ssh-privatekey": []byte("fake-key")},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(s).WithObjects(host, secret).WithStatusSubresource(host).Build()

	r := &LibvirtHostReconciler{
		Client: k8sClient,
		Scheme: s,
		SSHClientFactory: func(_ context.Context, _ *infrav1.LibvirtHost, _ *corev1.Secret) (*gossh.Client, error) {
			// SSH succeeds (return nil client — Close() is safe on nil per our impl)
			return nil, nil
		},
		LibvirtClientFactory: func(_ *gossh.Client, _ string) libvirt.Client {
			return &libvirt.MockClient{
				PingFn: func(_ context.Context) error {
					return fmt.Errorf("virsh: command not found")
				},
				CloseFn: func() error { return nil },
			}
		},
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "host1", Namespace: "default"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated := &infrav1.LibvirtHost{}
	_ = k8sClient.Get(context.Background(), types.NamespacedName{Name: "host1", Namespace: "default"}, updated)
	if updated.Status.Ready {
		t.Error("expected Ready=false when libvirt Ping fails")
	}
	if len(updated.Status.Conditions) == 0 {
		t.Fatal("expected condition to be set")
	}
	if updated.Status.Conditions[0].Reason != infrav1.ReasonConnectionFailed {
		t.Errorf("expected reason ConnectionFailed, got %s", updated.Status.Conditions[0].Reason)
	}
}

func TestHostReconcile_AllSucceeds_SetsReady(t *testing.T) {
	s := testScheme(t)
	host := &infrav1.LibvirtHost{
		ObjectMeta: metav1.ObjectMeta{Name: "host1", Namespace: "default"},
		Spec: infrav1.LibvirtHostSpec{
			URI:       "qemu+ssh://root@host/system",
			SecretRef: &infrav1.SecretReference{Name: "ssh-key"},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "ssh-key", Namespace: "default"},
		Data:       map[string][]byte{"ssh-privatekey": []byte("fake-key")},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(s).WithObjects(host, secret).WithStatusSubresource(host).Build()

	r := &LibvirtHostReconciler{
		Client: k8sClient,
		Scheme: s,
		SSHClientFactory: func(_ context.Context, _ *infrav1.LibvirtHost, _ *corev1.Secret) (*gossh.Client, error) {
			return nil, nil
		},
		LibvirtClientFactory: func(_ *gossh.Client, _ string) libvirt.Client {
			return &libvirt.MockClient{
				PingFn:  func(_ context.Context) error { return nil },
				CloseFn: func() error { return nil },
			}
		},
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "host1", Namespace: "default"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated := &infrav1.LibvirtHost{}
	_ = k8sClient.Get(context.Background(), types.NamespacedName{Name: "host1", Namespace: "default"}, updated)
	if !updated.Status.Ready {
		t.Error("expected Ready=true when SSH + libvirt Ping succeed")
	}
	if updated.Status.Conditions[0].Reason != infrav1.ReasonConnectionSucceeded {
		t.Errorf("expected reason ConnectionSucceeded, got %s", updated.Status.Conditions[0].Reason)
	}
}

// A host where libvirt answers but the QEMU/KVM hypervisor is missing (partial
// install) must be marked not-ready with HypervisorUnavailable, not pass as
// healthy and fail later at machine provision.
func TestHostReconcile_HypervisorUnavailable_SetsNotReady(t *testing.T) {
	s := testScheme(t)
	host := &infrav1.LibvirtHost{
		ObjectMeta: metav1.ObjectMeta{Name: "host1", Namespace: "default"},
		Spec: infrav1.LibvirtHostSpec{
			URI:       "qemu+ssh://root@host/system",
			SecretRef: &infrav1.SecretReference{Name: "ssh-key"},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "ssh-key", Namespace: "default"},
		Data:       map[string][]byte{"ssh-privatekey": []byte("fake-key")},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(s).WithObjects(host, secret).WithStatusSubresource(host).Build()

	r := &LibvirtHostReconciler{
		Client: k8sClient,
		Scheme: s,
		SSHClientFactory: func(_ context.Context, _ *infrav1.LibvirtHost, _ *corev1.Secret) (*gossh.Client, error) {
			return nil, nil
		},
		LibvirtClientFactory: func(_ *gossh.Client, _ string) libvirt.Client {
			return &libvirt.MockClient{
				PingFn:             func(_ context.Context) error { return nil },
				VerifyHypervisorFn: func(_ context.Context) error { return fmt.Errorf("failed to get emulator capabilities") },
				CloseFn:            func() error { return nil },
			}
		},
	}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "host1", Namespace: "default"},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated := &infrav1.LibvirtHost{}
	_ = k8sClient.Get(context.Background(), types.NamespacedName{Name: "host1", Namespace: "default"}, updated)
	if updated.Status.Ready {
		t.Error("expected Ready=false when the hypervisor is unavailable")
	}
	if updated.Status.Conditions[0].Reason != infrav1.ReasonHypervisorUnavailable {
		t.Errorf("expected reason HypervisorUnavailable, got %s", updated.Status.Conditions[0].Reason)
	}
}

// A session-mode host missing its unprivileged-networking setup (setuid
// qemu-bridge-helper, lingering) must be marked not-ready with
// SessionModeMisconfigured, not fail obscurely at first machine provision.
func TestHostReconcile_SessionPrerequisitesMissing_SetsNotReady(t *testing.T) {
	s := testScheme(t)
	host := &infrav1.LibvirtHost{
		ObjectMeta: metav1.ObjectMeta{Name: "host1", Namespace: "default"},
		Spec: infrav1.LibvirtHostSpec{
			URI:       "qemu+ssh://caplv@host/session",
			SecretRef: &infrav1.SecretReference{Name: "ssh-key"},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "ssh-key", Namespace: "default"},
		Data:       map[string][]byte{"ssh-privatekey": []byte("fake-key")},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(s).WithObjects(host, secret).WithStatusSubresource(host).Build()

	r := &LibvirtHostReconciler{
		Client: k8sClient,
		Scheme: s,
		SSHClientFactory: func(_ context.Context, _ *infrav1.LibvirtHost, _ *corev1.Secret) (*gossh.Client, error) {
			return nil, nil
		},
		LibvirtClientFactory: func(_ *gossh.Client, connURI string) libvirt.Client {
			if connURI != libvirt.ConnURISession {
				t.Errorf("factory got connURI %q, want %q", connURI, libvirt.ConnURISession)
			}
			return &libvirt.MockClient{
				VerifySessionPrerequisitesFn: func(_ context.Context) error {
					return fmt.Errorf("lingering is not enabled for caplv")
				},
			}
		},
	}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "host1", Namespace: "default"},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated := &infrav1.LibvirtHost{}
	_ = k8sClient.Get(context.Background(), types.NamespacedName{Name: "host1", Namespace: "default"}, updated)
	if updated.Status.Ready {
		t.Error("expected Ready=false when session prerequisites are missing")
	}
	if updated.Status.Conditions[0].Reason != infrav1.ReasonSessionModeMisconfigured {
		t.Errorf("expected reason SessionModeMisconfigured, got %s", updated.Status.Conditions[0].Reason)
	}
}

// System-mode hosts must not be probed for session prerequisites.
func TestHostReconcile_SystemHost_SkipsSessionCheck(t *testing.T) {
	s := testScheme(t)
	host := &infrav1.LibvirtHost{
		ObjectMeta: metav1.ObjectMeta{Name: "host1", Namespace: "default"},
		Spec: infrav1.LibvirtHostSpec{
			URI:       "qemu+ssh://caplv@host/system",
			SecretRef: &infrav1.SecretReference{Name: "ssh-key"},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "ssh-key", Namespace: "default"},
		Data:       map[string][]byte{"ssh-privatekey": []byte("fake-key")},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(s).WithObjects(host, secret).WithStatusSubresource(host).Build()

	r := &LibvirtHostReconciler{
		Client: k8sClient,
		Scheme: s,
		SSHClientFactory: func(_ context.Context, _ *infrav1.LibvirtHost, _ *corev1.Secret) (*gossh.Client, error) {
			return nil, nil
		},
		LibvirtClientFactory: func(_ *gossh.Client, _ string) libvirt.Client {
			return &libvirt.MockClient{
				VerifySessionPrerequisitesFn: func(_ context.Context) error {
					t.Error("VerifySessionPrerequisites must not be called for a /system host")
					return nil
				},
			}
		},
	}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "host1", Namespace: "default"},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated := &infrav1.LibvirtHost{}
	_ = k8sClient.Get(context.Background(), types.NamespacedName{Name: "host1", Namespace: "default"}, updated)
	if !updated.Status.Ready {
		t.Error("expected Ready=true for a healthy system-mode host")
	}
}
