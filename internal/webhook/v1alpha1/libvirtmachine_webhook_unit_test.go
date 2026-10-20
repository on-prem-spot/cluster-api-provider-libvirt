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
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = infrav1.AddToScheme(s)
	return s
}

func testMachineSpec() infrav1.LibvirtMachineSpec {
	return infrav1.LibvirtMachineSpec{
		HostRef: &infrav1.LibvirtHostReference{Name: "host-01"},
		Domain: infrav1.DomainSpec{
			VCPUs:    4,
			MemoryMB: 8192,
		},
		RootDisk: infrav1.RootDiskSpec{
			Size:        resource.MustParse("100Gi"),
			StoragePool: "default",
			BaseImage:   "rhcos-4.14.qcow2",
		},
		Network: infrav1.NetworkSpec{
			Type:      infrav1.NetworkTypeBridge,
			Name:      "br0",
			Addresses: []string{"192.168.1.50/24"},
		},
		BootstrapFormat: infrav1.BootstrapFormatIgnition,
	}
}

// --- ValidateCreate: address validation ---

func TestValidateCreate_ValidMachine(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	obj := &infrav1.LibvirtMachine{Spec: testMachineSpec()}
	_, err := v.ValidateCreate(context.Background(), obj)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

// Addresses may come from the host's spec.machineNetwork, which admission
// cannot see for a selector-placed machine.
func TestValidateCreate_AddressesMayComeFromHost(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	spec := testMachineSpec()
	spec.Network.Addresses = nil
	obj := &infrav1.LibvirtMachine{Spec: spec}
	if _, err := v.ValidateCreate(context.Background(), obj); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateCreate_Placement(t *testing.T) {
	for _, tc := range []struct {
		name     string
		hostRef  *infrav1.LibvirtHostReference
		selector *metav1.LabelSelector
		wantErr  string
	}{
		{name: "hostRef", hostRef: &infrav1.LibvirtHostReference{Name: "host-01"}},
		{name: "selector", selector: &metav1.LabelSelector{MatchLabels: map[string]string{"role": "worker"}}},
		{name: "empty selector matches every host", selector: &metav1.LabelSelector{}},
		{name: "neither", wantErr: "spec.hostRef"},
		{
			name: "both", hostRef: &infrav1.LibvirtHostReference{Name: "host-01"}, selector: &metav1.LabelSelector{},
			wantErr: "spec.hostSelector",
		},
		{
			name: "invalid selector",
			selector: &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{
				{Key: "role", Operator: "Near"},
			}},
			wantErr: "spec.hostSelector",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := testMachineSpec()
			spec.HostRef, spec.HostSelector = tc.hostRef, tc.selector
			_, err := (&LibvirtMachineCustomValidator{}).ValidateCreate(context.Background(), &infrav1.LibvirtMachine{Spec: spec})
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("got error %v, want one mentioning %s", err, tc.wantErr)
			}
		})
	}
}

// A pinned host already claimed by another machine through its selector is
// rejected at admission, not only by the controller.
func TestValidateCreate_PinnedHostClaimedByAnother(t *testing.T) {
	s := runtime.NewScheme()
	if err := infrav1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	host := &infrav1.LibvirtHost{
		ObjectMeta: metav1.ObjectMeta{Name: "host-01", Namespace: "default"},
		Spec:       infrav1.LibvirtHostSpec{ConsumerRef: &infrav1.LibvirtMachineReference{Name: "selected-vm"}},
	}
	v := &LibvirtMachineCustomValidator{Client: fake.NewClientBuilder().WithScheme(s).WithObjects(host).Build()}
	obj := &infrav1.LibvirtMachine{
		ObjectMeta: metav1.ObjectMeta{Name: "pinned-vm", Namespace: "default"},
		Spec:       testMachineSpec(), // hostRef: host-01
	}
	_, err := v.ValidateCreate(context.Background(), obj)
	if err == nil || !strings.Contains(err.Error(), "selected-vm") {
		t.Errorf("got error %v, want one naming the machine holding the host", err)
	}
}

func TestValidateCreate_InvalidCIDR(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	spec := testMachineSpec()
	spec.Network.Addresses = []string{"not-a-cidr"}
	obj := &infrav1.LibvirtMachine{Spec: spec}
	_, err := v.ValidateCreate(context.Background(), obj)
	if err == nil {
		t.Error("expected error for invalid CIDR")
	}
}

func TestValidateCreate_NoPrefixLength(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	spec := testMachineSpec()
	spec.Network.Addresses = []string{"192.168.1.50"}
	obj := &infrav1.LibvirtMachine{Spec: spec}
	_, err := v.ValidateCreate(context.Background(), obj)
	if err == nil {
		t.Error("expected error for address without prefix length")
	}
}

func TestValidateCreate_MultipleAddresses(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	spec := testMachineSpec()
	spec.Network.Addresses = []string{"192.168.1.50/24", "10.0.0.5/16"}
	obj := &infrav1.LibvirtMachine{Spec: spec}
	_, err := v.ValidateCreate(context.Background(), obj)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

// --- ValidateCreate: full-clone cross-pool rejection ---

func TestValidateCreate_FullCloneSamePool(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	spec := testMachineSpec()
	spec.RootDisk.CloneStrategy = infrav1.CloneStrategyFullClone
	// baseImagePool unset, defaults to storagePool — same pool, allowed.
	obj := &infrav1.LibvirtMachine{Spec: spec}
	_, err := v.ValidateCreate(context.Background(), obj)
	if err != nil {
		t.Errorf("expected no error for full-clone same pool, got: %v", err)
	}
}

func TestValidateCreate_FullCloneCrossPool_Rejected(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	spec := testMachineSpec()
	spec.RootDisk.CloneStrategy = infrav1.CloneStrategyFullClone
	spec.RootDisk.StoragePool = "ephemeral"
	spec.RootDisk.BaseImagePool = "default"
	obj := &infrav1.LibvirtMachine{Spec: spec}
	_, err := v.ValidateCreate(context.Background(), obj)
	if err == nil {
		t.Error("expected error for full-clone with cross-pool")
	}
}

func TestValidateCreate_CopyOnWriteCrossPool_Allowed(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	spec := testMachineSpec()
	spec.RootDisk.CloneStrategy = infrav1.CloneStrategyCopyOnWrite
	spec.RootDisk.StoragePool = "ephemeral"
	spec.RootDisk.BaseImagePool = "default"
	obj := &infrav1.LibvirtMachine{Spec: spec}
	_, err := v.ValidateCreate(context.Background(), obj)
	if err != nil {
		t.Errorf("expected no error for copy-on-write cross-pool, got: %v", err)
	}
}

func TestValidateCreate_RejectsInjectedStoragePool(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	spec := testMachineSpec()
	spec.RootDisk.StoragePool = "default; printf INJECTED"
	obj := &infrav1.LibvirtMachine{Spec: spec}
	_, err := v.ValidateCreate(context.Background(), obj)
	if err == nil {
		t.Error("expected error for storagePool with shell metacharacters")
	}
}

func TestValidateCreate_RejectsInjectedAdditionalDiskFields(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	spec := testMachineSpec()
	spec.AdditionalDisks = []infrav1.AdditionalDiskSpec{{
		Name:        "data; printf INJECTED",
		StoragePool: "default",
		Size:        resource.MustParse("10Gi"),
	}}
	obj := &infrav1.LibvirtMachine{Spec: spec}
	_, err := v.ValidateCreate(context.Background(), obj)
	if err == nil {
		t.Error("expected error for additional disk name with shell metacharacters")
	}
}

// --- ValidateCreate: host uniqueness ---

func TestValidateCreate_HostAlreadyInUse_Rejected(t *testing.T) {
	s := testScheme()
	existing := &infrav1.LibvirtMachine{
		ObjectMeta: metav1.ObjectMeta{Name: "existing-vm", Namespace: "default"},
		Spec:       testMachineSpec(),
	}
	k8sClient := fake.NewClientBuilder().WithScheme(s).WithObjects(existing).Build()

	v := &LibvirtMachineCustomValidator{Client: k8sClient}
	newObj := &infrav1.LibvirtMachine{
		ObjectMeta: metav1.ObjectMeta{Name: "new-vm", Namespace: "default"},
		Spec:       testMachineSpec(), // same hostRef: host-01
	}
	_, err := v.ValidateCreate(context.Background(), newObj)
	if err == nil {
		t.Error("expected error: host already in use")
	}
}

func TestValidateCreate_DifferentHost_Allowed(t *testing.T) {
	s := testScheme()
	existing := &infrav1.LibvirtMachine{
		ObjectMeta: metav1.ObjectMeta{Name: "existing-vm", Namespace: "default"},
		Spec:       testMachineSpec(), // hostRef: host-01
	}
	k8sClient := fake.NewClientBuilder().WithScheme(s).WithObjects(existing).Build()

	v := &LibvirtMachineCustomValidator{Client: k8sClient}
	spec := testMachineSpec()
	spec.HostRef.Name = "host-02" // different host
	newObj := &infrav1.LibvirtMachine{
		ObjectMeta: metav1.ObjectMeta{Name: "new-vm", Namespace: "default"},
		Spec:       spec,
	}
	_, err := v.ValidateCreate(context.Background(), newObj)
	if err != nil {
		t.Errorf("expected no error for different host, got: %v", err)
	}
}

func TestValidateCreate_DeletingMachineDoesNotBlock(t *testing.T) {
	s := testScheme()
	now := metav1.Now()
	existing := &infrav1.LibvirtMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "deleting-vm",
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{"test"}, // needed for DeletionTimestamp to be set
		},
		Spec: testMachineSpec(), // same host
	}
	k8sClient := fake.NewClientBuilder().WithScheme(s).WithObjects(existing).Build()

	v := &LibvirtMachineCustomValidator{Client: k8sClient}
	newObj := &infrav1.LibvirtMachine{
		ObjectMeta: metav1.ObjectMeta{Name: "new-vm", Namespace: "default"},
		Spec:       testMachineSpec(),
	}
	_, err := v.ValidateCreate(context.Background(), newObj)
	if err != nil {
		t.Errorf("expected no error: deleting machine should not block, got: %v", err)
	}
}

func TestValidateCreate_NoClient_SkipsHostCheck(t *testing.T) {
	v := &LibvirtMachineCustomValidator{} // nil client
	obj := &infrav1.LibvirtMachine{Spec: testMachineSpec()}
	_, err := v.ValidateCreate(context.Background(), obj)
	if err != nil {
		t.Errorf("expected no error with nil client, got: %v", err)
	}
}

// --- ValidateUpdate ---

func TestValidateUpdate_RejectSpecChange(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	oldObj := &infrav1.LibvirtMachine{Spec: testMachineSpec()}
	newSpec := testMachineSpec()
	newSpec.Domain.VCPUs = 8
	newObj := &infrav1.LibvirtMachine{Spec: newSpec}
	_, err := v.ValidateUpdate(context.Background(), oldObj, newObj)
	if err == nil {
		t.Error("expected error for spec change")
	}
}

func TestValidateUpdate_AllowProviderIDSetFromNil(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	oldObj := &infrav1.LibvirtMachine{Spec: testMachineSpec()}
	newSpec := testMachineSpec()
	newSpec.ProviderID = ptr.To("libvirt:///host/domain")
	newObj := &infrav1.LibvirtMachine{Spec: newSpec}
	_, err := v.ValidateUpdate(context.Background(), oldObj, newObj)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestValidateUpdate_RejectProviderIDChange(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	oldSpec := testMachineSpec()
	oldSpec.ProviderID = ptr.To("libvirt:///host/domain-old")
	oldObj := &infrav1.LibvirtMachine{Spec: oldSpec}
	newSpec := testMachineSpec()
	newSpec.ProviderID = ptr.To("libvirt:///host/domain-new")
	newObj := &infrav1.LibvirtMachine{Spec: newSpec}
	_, err := v.ValidateUpdate(context.Background(), oldObj, newObj)
	if err == nil {
		t.Error("expected error for providerID change")
	}
}

func TestValidateUpdate_RejectProviderIDClearing(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	oldSpec := testMachineSpec()
	oldSpec.ProviderID = ptr.To("libvirt:///host/domain")
	oldObj := &infrav1.LibvirtMachine{Spec: oldSpec}
	newObj := &infrav1.LibvirtMachine{Spec: testMachineSpec()}
	_, err := v.ValidateUpdate(context.Background(), oldObj, newObj)
	if err == nil {
		t.Error("expected error for clearing providerID")
	}
}

func TestValidateUpdate_AllowNoOpWithProviderID(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	spec := testMachineSpec()
	spec.ProviderID = ptr.To("libvirt:///host/domain")
	oldObj := &infrav1.LibvirtMachine{Spec: spec}
	newObj := &infrav1.LibvirtMachine{Spec: *spec.DeepCopy()}
	_, err := v.ValidateUpdate(context.Background(), oldObj, newObj)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

// --- Node label / annotation key restrictions ---
//
// The controller patches Nodes with its own cluster-scoped identity, so a
// LibvirtMachine author must not be able to write keys that decide scheduling,
// policy or node role. Third-party keys stay writable.

func TestValidateCreate_RejectsReservedNodeLabelKeys(t *testing.T) {
	for _, key := range []string{
		"node-role.kubernetes.io/master",
		"node-restriction.kubernetes.io/fips",
		"topology.kubernetes.io/zone",
		"kubernetes.io/os",
		"beta.kubernetes.io/arch",
		"foo.k8s.io/bar",
		"NODE-ROLE.KUBERNETES.IO/master",
	} {
		v := &LibvirtMachineCustomValidator{}
		spec := testMachineSpec()
		spec.NodeLabels = map[string]string{key: "x"}
		obj := &infrav1.LibvirtMachine{Spec: spec}
		if _, err := v.ValidateCreate(context.Background(), obj); err == nil {
			t.Errorf("expected reserved node label %q to be rejected", key)
		}
	}
}

func TestValidateCreate_AllowsThirdPartyNodeLabelKeys(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	spec := testMachineSpec()
	spec.NodeLabels = map[string]string{
		"k8s.ovn.org/egress-assignable": "",
		"dynatrace":                     "monitored",
		"aqua/scan":                     "enabled",
		"example.com/team":              "platform",
		"notk8s.io/x":                   "y",
	}
	obj := &infrav1.LibvirtMachine{Spec: spec}
	if _, err := v.ValidateCreate(context.Background(), obj); err != nil {
		t.Errorf("expected third-party node labels to be accepted, got: %v", err)
	}
}

// CAPLV records which keys it owns in these annotations. An author who could
// set them directly could name any pre-existing label as CAPLV-managed and have
// the next reconcile delete it.
func TestValidateCreate_RejectsCAPLVOwnershipAnnotations(t *testing.T) {
	for _, key := range []string{
		infrav1.ManagedNodeLabelsAnnotation,
		infrav1.ManagedNodeAnnotationsAnnotation,
	} {
		v := &LibvirtMachineCustomValidator{}
		spec := testMachineSpec()
		spec.NodeAnnotations = map[string]string{key: "some-other-label"}
		obj := &infrav1.LibvirtMachine{Spec: spec}
		if _, err := v.ValidateCreate(context.Background(), obj); err == nil {
			t.Errorf("expected reserved annotation %q to be rejected", key)
		}
	}
}

func TestValidateCreate_RejectsReservedNodeAnnotationKeys(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	spec := testMachineSpec()
	spec.NodeAnnotations = map[string]string{"kubernetes.io/config.source": "file"}
	obj := &infrav1.LibvirtMachine{Spec: spec}
	if _, err := v.ValidateCreate(context.Background(), obj); err == nil {
		t.Error("expected reserved node annotation to be rejected")
	}
}

// A machine created before this restriction existed must keep reconciling. The
// reconciler writes spec.providerID, which is an update, so re-checking keys it
// is merely carrying forward would wedge the machine short of Ready.
func TestValidateUpdate_AllowsCarryingForwardAPreExistingReservedKey(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	spec := testMachineSpec()
	spec.NodeLabels = map[string]string{"node-role.kubernetes.io/worker": ""}
	old := &infrav1.LibvirtMachine{Spec: spec}

	updated := &infrav1.LibvirtMachine{Spec: *spec.DeepCopy()}
	updated.Spec.ProviderID = ptr.To("libvirt:///host-01/ns-cluster-machine")

	if _, err := v.ValidateUpdate(context.Background(), old, updated); err != nil {
		t.Errorf("expected the providerID write to be allowed, got: %v", err)
	}
}

// Introducing a reserved key on update is still refused. Spec immutability
// would also catch this today; the check is kept so the guard survives that
// rule being relaxed, hence the assertion on which error fired.
func TestValidateUpdate_RejectsNewlyAddedReservedNodeLabelKey(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	old := &infrav1.LibvirtMachine{Spec: testMachineSpec()}

	spec := testMachineSpec()
	spec.NodeLabels = map[string]string{"node-role.kubernetes.io/master": ""}
	updated := &infrav1.LibvirtMachine{Spec: spec}

	_, err := v.ValidateUpdate(context.Background(), old, updated)
	if err == nil {
		t.Fatal("expected a newly added reserved node label to be rejected")
	}
	if !strings.Contains(err.Error(), "spec.nodeLabels[node-role.kubernetes.io/master]") {
		t.Errorf("expected the nodeLabels check to fire, got: %v", err)
	}
}

// Changing the value of a reserved key is a fresh assertion, not a carry-over.
func TestValidateUpdate_RejectsChangedReservedNodeLabelValue(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	spec := testMachineSpec()
	spec.NodeLabels = map[string]string{"node-restriction.kubernetes.io/pool": "a"}
	old := &infrav1.LibvirtMachine{Spec: spec}

	changed := *spec.DeepCopy()
	changed.NodeLabels = map[string]string{"node-restriction.kubernetes.io/pool": "b"}
	updated := &infrav1.LibvirtMachine{Spec: changed}

	_, err := v.ValidateUpdate(context.Background(), old, updated)
	if err == nil {
		t.Fatal("expected a changed reserved node label value to be rejected")
	}
	if !strings.Contains(err.Error(), "spec.nodeLabels[node-restriction.kubernetes.io/pool]") {
		t.Errorf("expected the nodeLabels check to fire, got: %v", err)
	}
}
