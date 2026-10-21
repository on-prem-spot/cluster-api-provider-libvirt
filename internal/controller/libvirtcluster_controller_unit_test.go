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
	"testing"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestClusterReadiness(t *testing.T) {
	for _, tc := range []struct {
		name, host                        string
		port                              int32
		paused, external, deleting, ready bool
	}{
		{name: "configured unreachable endpoint", host: "127.0.0.1", port: 1, ready: true},
		{name: "missing host", port: 6443},
		{name: "missing port", host: "127.0.0.1"},
		{name: "invalid port", host: "127.0.0.1", port: 65536},
		{name: "paused", host: "127.0.0.1", port: 1, paused: true},
		{name: "externally managed", host: "127.0.0.1", port: 1, external: true},
		{name: "deleting", host: "127.0.0.1", port: 1, deleting: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := runtime.NewScheme()
			if err := infrav1.AddToScheme(s); err != nil {
				t.Fatal(err)
			}
			if err := clusterv1.AddToScheme(s); err != nil {
				t.Fatal(err)
			}
			owner := &clusterv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "owner", Namespace: "default"}, Spec: clusterv1.ClusterSpec{Paused: &tc.paused}}
			obj := &infrav1.LibvirtCluster{ObjectMeta: metav1.ObjectMeta{Name: "infra", Namespace: "default", Generation: 3, OwnerReferences: []metav1.OwnerReference{{APIVersion: clusterv1.GroupVersion.String(), Kind: "Cluster", Name: owner.Name}}}, Spec: infrav1.LibvirtClusterSpec{ControlPlaneEndpoint: infrav1.APIEndpoint{Host: tc.host, Port: tc.port}}}
			if tc.external {
				obj.Annotations = map[string]string{clusterv1.ManagedByAnnotation: "other"}
			}
			if tc.deleting {
				now := metav1.Now()
				obj.DeletionTimestamp = &now
				obj.Finalizers = []string{"test"}
			}
			owner.Spec.InfrastructureRef = clusterv1.ContractVersionedObjectReference{APIGroup: infrav1.GroupVersion.Group, Kind: "LibvirtCluster", Name: obj.Name}
			c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(obj).WithObjects(owner, obj).Build()
			r := &LibvirtClusterReconciler{Client: c, Scheme: s}
			key := types.NamespacedName{Namespace: obj.Namespace, Name: obj.Name}
			for range 2 {
				result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
				if err != nil {
					t.Fatal(err)
				}
				if !result.IsZero() {
					t.Fatalf("unexpected periodic requeue: %+v", result)
				}
			}
			got := &infrav1.LibvirtCluster{}
			if err := c.Get(context.Background(), key, got); err != nil {
				t.Fatal(err)
			}
			if got.Status.Ready != tc.ready {
				t.Fatalf("ready=%v, want %v", got.Status.Ready, tc.ready)
			}
			provisioned := got.Status.Initialization != nil && ptr.Deref(got.Status.Initialization.Provisioned, false)
			if provisioned != tc.ready {
				t.Fatalf("initialization.provisioned=%v, want %v", provisioned, tc.ready)
			}
			if tc.external {
				if len(got.Status.Conditions) != 0 {
					t.Fatal("externally managed resource status changed")
				}
			} else if p := meta.FindStatusCondition(got.Status.Conditions, clusterv1.PausedCondition); p == nil ||
				(p.Status == metav1.ConditionTrue) != tc.paused {
				t.Fatalf("incorrect Paused condition: %+v", p)
			}
			if tc.paused || tc.external || tc.deleting {
				if meta.FindStatusCondition(got.Status.Conditions, infrav1.ReadyCondition) != nil {
					t.Fatal("ignored resource reported a Ready condition")
				}
			} else if cond := meta.FindStatusCondition(got.Status.Conditions, "Ready"); cond == nil || (cond.Status == metav1.ConditionTrue) != tc.ready || cond.ObservedGeneration != 3 {
				t.Fatalf("incorrect condition: %+v", cond)
			}
		})
	}
}

func TestClusterStatusPatchFailure(t *testing.T) {
	s := runtime.NewScheme()
	if err := infrav1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := clusterv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	owner := &clusterv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "owner", Namespace: "default"}}
	obj := &infrav1.LibvirtCluster{ObjectMeta: metav1.ObjectMeta{Name: "infra", Namespace: "default", OwnerReferences: []metav1.OwnerReference{{APIVersion: clusterv1.GroupVersion.String(), Kind: "Cluster", Name: owner.Name}}}, Spec: infrav1.LibvirtClusterSpec{ControlPlaneEndpoint: infrav1.APIEndpoint{Host: "127.0.0.1", Port: 1}}}
	patchErr := errors.New("status write unavailable")
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(obj).WithObjects(owner, obj).WithInterceptorFuncs(interceptor.Funcs{
		SubResourcePatch: func(context.Context, client.Client, string, client.Object, client.Patch, ...client.SubResourcePatchOption) error {
			return patchErr
		},
	}).Build()
	r := &LibvirtClusterReconciler{Client: c, Scheme: s}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)}); err == nil {
		t.Fatal("status patch failure must trigger retry")
	}
}

func TestExternallyManagedResourcesAreUntouched(t *testing.T) {
	s := runtime.NewScheme()
	if err := infrav1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	for _, obj := range []client.Object{&infrav1.LibvirtHost{}, &infrav1.LibvirtMachine{}} {
		obj.SetName("external")
		obj.SetNamespace("default")
		obj.SetAnnotations(map[string]string{clusterv1.ManagedByAnnotation: "other"})
		c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(obj).WithObjects(obj).WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
				t.Fatal("externally managed object patched")
				return nil
			},
			SubResourcePatch: func(context.Context, client.Client, string, client.Object, client.Patch, ...client.SubResourcePatchOption) error {
				t.Fatal("externally managed status patched")
				return nil
			},
		}).Build()
		req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)}
		var result ctrl.Result
		var err error
		switch obj.(type) {
		case *infrav1.LibvirtHost:
			result, err = (&LibvirtHostReconciler{Client: c, Scheme: s}).Reconcile(context.Background(), req)
		case *infrav1.LibvirtMachine:
			result, err = (&LibvirtMachineReconciler{Client: c, Scheme: s}).Reconcile(context.Background(), req)
		}
		if err != nil || !result.IsZero() {
			t.Fatalf("external resource reconciled: %+v, %v", result, err)
		}
	}
}

func TestClusterReportsHostsAsFailureDomains(t *testing.T) {
	s := runtime.NewScheme()
	if err := infrav1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := clusterv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	owner := &clusterv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "owner", Namespace: "default"}}
	obj := &infrav1.LibvirtCluster{ObjectMeta: metav1.ObjectMeta{Name: "infra", Namespace: "default", OwnerReferences: []metav1.OwnerReference{{APIVersion: clusterv1.GroupVersion.String(), Kind: "Cluster", Name: owner.Name}}}}
	now := metav1.Now()
	hosts := []client.Object{
		&infrav1.LibvirtHost{ObjectMeta: metav1.ObjectMeta{Name: "host-a", Namespace: "default"}},
		&infrav1.LibvirtHost{ObjectMeta: metav1.ObjectMeta{Name: "host-b", Namespace: "default",
			Labels: map[string]string{clusterv1.WatchLabel: "other"}}},
		&infrav1.LibvirtHost{ObjectMeta: metav1.ObjectMeta{Name: "host-gone", Namespace: "default",
			DeletionTimestamp: &now, Finalizers: []string{infrav1.HostFinalizer}}},
		&infrav1.LibvirtHost{ObjectMeta: metav1.ObjectMeta{Name: "host-elsewhere", Namespace: "other"}},
		&infrav1.LibvirtHost{ObjectMeta: metav1.ObjectMeta{Name: "host-cordoned", Namespace: "default"},
			Spec: infrav1.LibvirtHostSpec{Unschedulable: true}},
	}

	for _, tc := range []struct {
		filter string
		want   []string
	}{
		{filter: "", want: []string{"host-a", "host-b"}},
		{filter: "other", want: []string{"host-b"}},
	} {
		c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(obj).WithObjects(append(hosts, owner, obj.DeepCopy())...).Build()
		r := &LibvirtClusterReconciler{Client: c, Scheme: s, WatchFilterValue: tc.filter}
		if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)}); err != nil {
			t.Fatal(err)
		}
		got := &infrav1.LibvirtCluster{}
		if err := c.Get(context.Background(), client.ObjectKeyFromObject(obj), got); err != nil {
			t.Fatal(err)
		}
		if len(got.Status.FailureDomains) != len(tc.want) {
			t.Fatalf("filter %q: failureDomains=%v, want %v", tc.filter, got.Status.FailureDomains, tc.want)
		}
		for i, name := range tc.want {
			fd := got.Status.FailureDomains[i]
			if fd.Name != name || !ptr.Deref(fd.ControlPlane, false) {
				t.Errorf("filter %q: failure domain %d = %+v, want control-plane eligible %q", tc.filter, i, fd, name)
			}
		}
	}
}

func TestMachineReportsHostAsFailureDomain(t *testing.T) {
	lm := &infrav1.LibvirtMachine{ObjectMeta: metav1.ObjectMeta{Name: "worker01", Namespace: "default"},
		Spec: infrav1.LibvirtMachineSpec{HostRef: &infrav1.LibvirtHostReference{Name: "host-a"}}}
	host := &infrav1.LibvirtHost{ObjectMeta: metav1.ObjectMeta{Name: "host-a", Namespace: "default"}}
	r := &LibvirtMachineReconciler{}
	// An unready host returns before any connection is attempted.
	if _, err := r.reconcileNormal(context.Background(), lm, &clusterv1.Machine{}, &clusterv1.Cluster{}, &infrav1.LibvirtCluster{}, host); err != nil {
		t.Fatal(err)
	}
	if lm.Status.FailureDomain != "host-a" {
		t.Errorf("status.failureDomain = %q, want host-a", lm.Status.FailureDomain)
	}
}

// With spec.controlPlaneHostSelector, only matching hosts are control plane
// eligible, so KubeadmControlPlane never hands a control plane machine a
// worker host it cannot select.
func TestClusterMarksControlPlaneHosts(t *testing.T) {
	s := runtime.NewScheme()
	if err := infrav1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := clusterv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	owner := &clusterv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "owner", Namespace: "default"}}
	obj := &infrav1.LibvirtCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "infra", Namespace: "default", OwnerReferences: []metav1.OwnerReference{
			{APIVersion: clusterv1.GroupVersion.String(), Kind: "Cluster", Name: owner.Name},
		}},
		Spec: infrav1.LibvirtClusterSpec{ControlPlaneHostSelector: &metav1.LabelSelector{
			MatchLabels: map[string]string{"role": "control-plane"},
		}},
	}
	cp := &infrav1.LibvirtHost{ObjectMeta: metav1.ObjectMeta{Name: "host-cp", Namespace: "default",
		Labels: map[string]string{"role": "control-plane"}}}
	worker := &infrav1.LibvirtHost{ObjectMeta: metav1.ObjectMeta{Name: "host-worker", Namespace: "default",
		Labels: map[string]string{"role": "worker"}}}
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(obj).WithObjects(owner, obj, cp, worker).Build()
	r := &LibvirtClusterReconciler{Client: c, Scheme: s}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)}); err != nil {
		t.Fatal(err)
	}
	got := &infrav1.LibvirtCluster{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(obj), got); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"host-cp": true, "host-worker": false}
	if len(got.Status.FailureDomains) != len(want) {
		t.Fatalf("failureDomains = %v, want both hosts", got.Status.FailureDomains)
	}
	for _, fd := range got.Status.FailureDomains {
		if ptr.Deref(fd.ControlPlane, false) != want[fd.Name] {
			t.Errorf("failure domain %s: controlPlane = %v, want %v", fd.Name, ptr.Deref(fd.ControlPlane, false), want[fd.Name])
		}
	}
}
