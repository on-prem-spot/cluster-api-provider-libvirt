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
	"testing"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
	"github.com/on-prem-spot/cluster-api-provider-libvirt/internal/libvirt"
)

// Pool names reused across the preflight test cases.
const (
	poolImages  = "images"
	poolPersist = "persist"
	poolVMData  = "vmdata"
)

// poolRecorder returns a MockClient whose PoolExists reports every pool in
// present as existing, records the pools queried, and treats all others as
// missing.
func poolRecorder(present map[string]bool, queried *[]string) *libvirt.MockClient {
	return &libvirt.MockClient{
		PoolExistsFn: func(_ context.Context, name string) (bool, error) {
			*queried = append(*queried, name)
			return present[name], nil
		},
	}
}

func baseRC(client libvirt.Client) *reconcileCtx {
	return &reconcileCtx{
		libvirtMachine: &infrav1.LibvirtMachine{},
		libvirtCluster: &infrav1.LibvirtCluster{},
		libvirtHost:    &infrav1.LibvirtHost{ObjectMeta: metav1.ObjectMeta{Name: "host-a"}},
		libvirtClient:  client,
	}
}

func assertTerminal(t *testing.T, lm *infrav1.LibvirtMachine, reason string) {
	t.Helper()
	fr := lm.Status.FailureReason
	if fr == nil || *fr != reason {
		t.Fatalf("FailureReason = %v, want %q", fr, reason)
	}
	if lm.Status.FailureMessage == nil {
		t.Error("FailureMessage not set")
	}
	cond := apimeta.FindStatusCondition(lm.Status.Conditions, infrav1.InfrastructureReadyCondition)
	if cond == nil {
		t.Fatal("InfrastructureReady condition not set")
	}
	if cond.Status != metav1.ConditionFalse {
		t.Errorf("InfrastructureReady status = %v, want False", cond.Status)
	}
	if cond.Reason != reason {
		t.Errorf("InfrastructureReady reason = %q, want %q", cond.Reason, reason)
	}
}

// A missing base-image staging pool is the original bug: the cluster's
// baseImage.pool doesn't exist on the host. Must be terminal, not a retry loop.
func TestPreflightPools_BaseImagePoolNotFound(t *testing.T) {
	var queried []string
	rc := baseRC(poolRecorder(map[string]bool{}, &queried))
	rc.libvirtCluster.Spec.BaseImage = &infrav1.BaseImageSpec{Pool: "default", VolumeName: "rhcos.qcow2"}

	r := &LibvirtMachineReconciler{}
	if err := r.preflightPools(context.Background(), rc); err != nil {
		t.Fatalf("preflightPools returned error, want nil (terminal handled inline): %v", err)
	}
	assertTerminal(t, rc.libvirtMachine, infrav1.ReasonBaseImagePoolNotFound)
	if len(queried) == 0 || queried[0] != "default" {
		t.Errorf("first pool queried = %v, want \"default\"", queried)
	}
}

// A missing root-disk storagePool is the sibling bug — same opaque failure,
// different pool. Distinct reason so the message points at the right field.
func TestPreflightPools_StoragePoolNotFound(t *testing.T) {
	var queried []string
	rc := baseRC(poolRecorder(map[string]bool{poolImages: true}, &queried))
	rc.baseImagePool = poolImages
	rc.storagePool = poolVMData // missing on host

	r := &LibvirtMachineReconciler{}
	if err := r.preflightPools(context.Background(), rc); err != nil {
		t.Fatalf("preflightPools returned error: %v", err)
	}
	assertTerminal(t, rc.libvirtMachine, infrav1.ReasonStoragePoolNotFound)
}

// An additional-disk pool that doesn't exist is also caught up front.
func TestPreflightPools_AdditionalDiskPoolNotFound(t *testing.T) {
	var queried []string
	rc := baseRC(poolRecorder(map[string]bool{poolImages: true}, &queried))
	rc.baseImagePool = poolImages
	rc.storagePool = poolImages
	rc.libvirtMachine.Spec.AdditionalDisks = []infrav1.AdditionalDiskSpec{
		{Size: resource.MustParse("10Gi"), StoragePool: "scratch"},
	}

	r := &LibvirtMachineReconciler{}
	if err := r.preflightPools(context.Background(), rc); err != nil {
		t.Fatalf("preflightPools returned error: %v", err)
	}
	assertTerminal(t, rc.libvirtMachine, infrav1.ReasonStoragePoolNotFound)
}

// When ephemeralPool is set, the root-disk pool is a tmpfs pool CAPLV creates
// later — preflight must NOT check it (it legitimately doesn't exist yet).
func TestPreflightPools_EphemeralPoolSkipped(t *testing.T) {
	var queried []string
	rc := baseRC(poolRecorder(map[string]bool{poolImages: true}, &queried))
	rc.baseImagePool = poolImages
	rc.storagePool = "eph-machine-xyz" // the ephemeral pool, not yet created
	rc.libvirtMachine.Spec.RootDisk.EphemeralPool = true

	r := &LibvirtMachineReconciler{}
	if err := r.preflightPools(context.Background(), rc); err != nil {
		t.Fatalf("preflightPools returned error: %v", err)
	}
	if rc.libvirtMachine.Status.FailureReason != nil {
		t.Errorf("unexpected terminal error: %q", *rc.libvirtMachine.Status.FailureReason)
	}
	for _, q := range queried {
		if q == "eph-machine-xyz" {
			t.Error("ephemeral pool must not be preflight-checked")
		}
	}
}

// Everything present: no terminal error, all referenced pools verified once.
func TestPreflightPools_AllExist(t *testing.T) {
	var queried []string
	rc := baseRC(poolRecorder(map[string]bool{poolPersist: true, poolVMData: true}, &queried))
	rc.libvirtCluster.Spec.BaseImage = &infrav1.BaseImageSpec{Pool: poolPersist, VolumeName: "rhcos.qcow2"}
	rc.baseImagePool = poolPersist
	rc.storagePool = poolVMData

	r := &LibvirtMachineReconciler{}
	if err := r.preflightPools(context.Background(), rc); err != nil {
		t.Fatalf("preflightPools returned error: %v", err)
	}
	if rc.libvirtMachine.Status.FailureReason != nil {
		t.Errorf("unexpected terminal error: %q", *rc.libvirtMachine.Status.FailureReason)
	}
	// persist is referenced twice (cluster baseImage + rootDisk baseImagePool)
	// but must be de-duplicated to a single query.
	count := 0
	for _, q := range queried {
		if q == poolPersist {
			count++
		}
	}
	if count != 1 {
		t.Errorf("pool \"persist\" queried %d times, want 1 (dedup)", count)
	}
}

// A libvirt-managed network on a session-mode host can never start — the
// per-user daemon has no network driver. Must be terminal, not a retry loop
// on virsh start's "Network not found".
func TestPreflightSessionMode_NetworkTypeNetworkRejected(t *testing.T) {
	rc := baseRC(&libvirt.MockClient{})
	rc.libvirtHost.Spec.URI = "qemu+ssh://caplv@host/session"
	rc.libvirtMachine.Spec.Network.Type = infrav1.NetworkTypeNetwork

	r := &LibvirtMachineReconciler{}
	r.preflightSessionMode(context.Background(), rc)
	assertTerminal(t, rc.libvirtMachine, infrav1.ReasonNetworkTypeUnsupported)
}

// Bridge attachment is the supported session-mode path (via
// qemu-bridge-helper) and must pass preflight.
func TestPreflightSessionMode_BridgeAllowed(t *testing.T) {
	rc := baseRC(&libvirt.MockClient{})
	rc.libvirtHost.Spec.URI = "qemu+ssh://caplv@host/session"
	rc.libvirtMachine.Spec.Network.Type = infrav1.NetworkTypeBridge

	r := &LibvirtMachineReconciler{}
	r.preflightSessionMode(context.Background(), rc)
	if rc.libvirtMachine.Status.FailureReason != nil {
		t.Errorf("unexpected terminal error: %q", *rc.libvirtMachine.Status.FailureReason)
	}
}

// System-mode hosts support both attachment types; the check must not fire.
func TestPreflightSessionMode_SystemHostUnaffected(t *testing.T) {
	rc := baseRC(&libvirt.MockClient{})
	rc.libvirtHost.Spec.URI = "qemu+ssh://caplv@host/system"
	rc.libvirtMachine.Spec.Network.Type = infrav1.NetworkTypeNetwork

	r := &LibvirtMachineReconciler{}
	r.preflightSessionMode(context.Background(), rc)
	if rc.libvirtMachine.Status.FailureReason != nil {
		t.Errorf("unexpected terminal error: %q", *rc.libvirtMachine.Status.FailureReason)
	}
}
