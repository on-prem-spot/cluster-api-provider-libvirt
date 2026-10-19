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

package v1alpha1

import (
	"context"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

func TestDefaultBaseImagePoolFollowsStoragePool(t *testing.T) {
	d := &LibvirtMachineCustomDefaulter{}
	obj := &infrav1.LibvirtMachine{Spec: testMachineSpec()}
	obj.Spec.RootDisk.BaseImagePool = ""

	if err := d.Default(context.Background(), obj); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := obj.Spec.RootDisk.BaseImagePool; got != obj.Spec.RootDisk.StoragePool {
		t.Errorf("baseImagePool = %q, want the storage pool %q", got, obj.Spec.RootDisk.StoragePool)
	}
}

func TestDefaultBaseImagePoolLeavesExplicitValue(t *testing.T) {
	d := &LibvirtMachineCustomDefaulter{}
	obj := &infrav1.LibvirtMachine{Spec: testMachineSpec()}
	obj.Spec.RootDisk.BaseImagePool = "images"

	if err := d.Default(context.Background(), obj); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if obj.Spec.RootDisk.BaseImagePool != "images" {
		t.Errorf("an explicit pool was overwritten: %q", obj.Spec.RootDisk.BaseImagePool)
	}
}

// Zero vCPUs and memory are the documented request to size the VM from the
// host's spare capacity. A default here would silently disable that, so this
// test exists to fail if one is ever added.
func TestDefaultLeavesAutoSizingAlone(t *testing.T) {
	d := &LibvirtMachineCustomDefaulter{}
	obj := &infrav1.LibvirtMachine{Spec: testMachineSpec()}
	obj.Spec.Domain.VCPUs = 0
	obj.Spec.Domain.MemoryMB = 0

	if err := d.Default(context.Background(), obj); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if obj.Spec.Domain.VCPUs != 0 {
		t.Errorf("vcpus was defaulted to %d, which disables host-capacity auto-sizing", obj.Spec.Domain.VCPUs)
	}
	if obj.Spec.Domain.MemoryMB != 0 {
		t.Errorf("memoryMB was defaulted to %d, which disables host-capacity auto-sizing", obj.Spec.Domain.MemoryMB)
	}
}

// Empty means the tmpfs mount omits its size argument, so the kernel default
// applies. Deriving a size would change behaviour rather than record it.
func TestDefaultLeavesEphemeralPoolSizeEmpty(t *testing.T) {
	d := &LibvirtMachineCustomDefaulter{}
	obj := &infrav1.LibvirtMachine{Spec: testMachineSpec()}
	obj.Spec.RootDisk.EphemeralPool = true
	obj.Spec.RootDisk.EphemeralPoolSize = ""

	if err := d.Default(context.Background(), obj); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if obj.Spec.RootDisk.EphemeralPoolSize != "" {
		t.Errorf("ephemeralPoolSize was defaulted to %q, changing the kernel-default behaviour",
			obj.Spec.RootDisk.EphemeralPoolSize)
	}
}

// The defaulted value must satisfy the validator that runs after it, so a
// machine that is accepted when written by hand is still accepted once
// defaulting has filled the pool in.
func TestDefaultedMachineStillValidates(t *testing.T) {
	d := &LibvirtMachineCustomDefaulter{}
	obj := &infrav1.LibvirtMachine{Spec: testMachineSpec()}
	obj.Spec.RootDisk.BaseImagePool = ""
	obj.Spec.RootDisk.CloneStrategy = infrav1.CloneStrategyFullClone

	if err := d.Default(context.Background(), obj); err != nil {
		t.Fatalf("defaulting failed: %v", err)
	}
	v := &LibvirtMachineCustomValidator{}
	if _, err := v.ValidateCreate(context.Background(), obj); err != nil {
		t.Errorf("defaulted machine failed validation: %v", err)
	}
}

// A machine stored before defaulting existed carries an empty baseImagePool.
// Updating it (e.g. the controller adding its finalizer) must not trip the
// immutable-spec check.
func TestDefaultSkipsUpdateOfUndefaultedMachine(t *testing.T) {
	old := &infrav1.LibvirtMachine{Spec: testMachineSpec()}
	old.Spec.RootDisk.BaseImagePool = ""
	updated := old.DeepCopy()
	updated.Finalizers = []string{infrav1.MachineFinalizer}

	ctx := admission.NewContextWithRequest(context.Background(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Update},
	})
	if err := (&LibvirtMachineCustomDefaulter{}).Default(ctx, updated); err != nil {
		t.Fatalf("defaulting failed: %v", err)
	}
	if _, err := (&LibvirtMachineCustomValidator{}).ValidateUpdate(ctx, old, updated); err != nil {
		t.Errorf("update of undefaulted machine rejected: %v", err)
	}
}
