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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
	"github.com/on-prem-spot/cluster-api-provider-libvirt/internal/libvirt"
	"github.com/on-prem-spot/cluster-api-provider-libvirt/internal/scope"
)

// rootDiskRC builds a reconcileCtx for an ephemeral-pool machine whose root
// disk already exists, so reconcileRootDisk exercises only the pool logic.
func rootDiskRC(t *testing.T, client *libvirt.MockClient) *reconcileCtx {
	t.Helper()
	libvirtMachine := &infrav1.LibvirtMachine{
		ObjectMeta: metav1.ObjectMeta{Name: "m0", Namespace: "ns"},
		Spec:       infrav1.LibvirtMachineSpec{RootDisk: infrav1.RootDiskSpec{EphemeralPool: true}},
	}
	ms, err := scope.NewMachineScope(scope.MachineScopeParams{
		Client:         fake.NewClientBuilder().Build(),
		Cluster:        &clusterv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "c0", Namespace: "ns"}},
		Machine:        &clusterv1.Machine{},
		LibvirtCluster: &infrav1.LibvirtCluster{},
		LibvirtMachine: libvirtMachine,
		LibvirtHost:    &infrav1.LibvirtHost{ObjectMeta: metav1.ObjectMeta{Name: "host-a"}},
	})
	if err != nil {
		t.Fatalf("NewMachineScope: %v", err)
	}
	client.VolumeExistsFn = func(_ context.Context, _, _ string) (bool, error) { return true, nil }
	return &reconcileCtx{
		libvirtMachine: libvirtMachine,
		libvirtCluster: &infrav1.LibvirtCluster{},
		libvirtHost:    &infrav1.LibvirtHost{ObjectMeta: metav1.ObjectMeta{Name: "host-a"}},
		machineScope:   ms,
		libvirtClient:  client,
		rootDiskVolume: "m0-root.qcow2",
	}
}

// An ephemeral pool that exists and is running needs no repair.
func TestReconcileRootDisk_EphemeralPoolActive(t *testing.T) {
	started, created := false, false
	client := &libvirt.MockClient{
		PoolExistsFn:   func(_ context.Context, _ string) (bool, error) { return true, nil },
		PoolIsActiveFn: func(_ context.Context, _ string) (bool, error) { return true, nil },
		StartPoolFn:    func(_ context.Context, _ string) error { started = true; return nil },
		CreateTmpfsPoolFn: func(_ context.Context, _, _, _ string) error {
			created = true
			return nil
		},
	}
	rc := rootDiskRC(t, client)

	r := &LibvirtMachineReconciler{}
	if err := r.reconcileRootDisk(context.Background(), rc); err != nil {
		t.Fatalf("reconcileRootDisk returned error: %v", err)
	}
	if started || created {
		t.Errorf("active pool must be left alone: started=%v created=%v", started, created)
	}
}

// A defined-but-inactive pool (session daemon restarted, tmpfs still mounted)
// is repaired by pool-start alone — no destructive recreate.
func TestReconcileRootDisk_EphemeralPoolInactiveStarted(t *testing.T) {
	started, destroyed, created := false, false, false
	client := &libvirt.MockClient{
		PoolExistsFn:   func(_ context.Context, _ string) (bool, error) { return true, nil },
		PoolIsActiveFn: func(_ context.Context, _ string) (bool, error) { return false, nil },
		StartPoolFn:    func(_ context.Context, _ string) error { started = true; return nil },
		DestroyPoolFn:  func(_ context.Context, _ string) error { destroyed = true; return nil },
		CreateTmpfsPoolFn: func(_ context.Context, _, _, _ string) error {
			created = true
			return nil
		},
	}
	rc := rootDiskRC(t, client)

	r := &LibvirtMachineReconciler{}
	if err := r.reconcileRootDisk(context.Background(), rc); err != nil {
		t.Fatalf("reconcileRootDisk returned error: %v", err)
	}
	if !started {
		t.Error("inactive pool was not started")
	}
	if destroyed || created {
		t.Errorf("startable pool must not be recreated: destroyed=%v created=%v", destroyed, created)
	}
}

// When pool-start fails (tmpfs backing gone), the stale pool is destroyed and
// recreated instead of retrying "pool is not active" forever.
func TestReconcileRootDisk_EphemeralPoolInactiveRecreated(t *testing.T) {
	destroyed, created := false, false
	client := &libvirt.MockClient{
		PoolExistsFn:   func(_ context.Context, _ string) (bool, error) { return true, nil },
		PoolIsActiveFn: func(_ context.Context, _ string) (bool, error) { return false, nil },
		StartPoolFn: func(_ context.Context, _ string) error {
			return fmt.Errorf("pool-start failed: cannot open directory")
		},
		DestroyPoolFn: func(_ context.Context, _ string) error { destroyed = true; return nil },
		CreateTmpfsPoolFn: func(_ context.Context, _, _, _ string) error {
			created = true
			return nil
		},
	}
	rc := rootDiskRC(t, client)

	r := &LibvirtMachineReconciler{}
	if err := r.reconcileRootDisk(context.Background(), rc); err != nil {
		t.Fatalf("reconcileRootDisk returned error: %v", err)
	}
	if !destroyed {
		t.Error("stale pool was not destroyed")
	}
	if !created {
		t.Error("stale pool was not recreated")
	}
}
