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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

const claimNamespace = "team-a"

func claimHostFixture(name string, ready bool, labels map[string]string) *infrav1.LibvirtHost {
	return &infrav1.LibvirtHost{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: claimNamespace, Labels: labels},
		Status: infrav1.LibvirtHostStatus{
			Ready:    ready,
			Capacity: &infrav1.HostCapacity{AvailableVCPUs: 4, AvailableMemoryMB: 8192},
		},
	}
}

func selectorMachine(name string) *infrav1.LibvirtMachine {
	return &infrav1.LibvirtMachine{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: claimNamespace},
		Spec: infrav1.LibvirtMachineSpec{
			HostSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"role": "worker"}},
		},
	}
}

func claimReconciler(t *testing.T, objs ...client.Object) (*LibvirtMachineReconciler, client.Client) {
	t.Helper()
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(objs...).
		WithStatusSubresource(&infrav1.LibvirtHost{}).Build()
	return &LibvirtMachineReconciler{Client: c, Scheme: testScheme(t)}, c
}

func hostConsumer(t *testing.T, c client.Client, name string) string {
	t.Helper()
	host := &infrav1.LibvirtHost{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: claimNamespace, Name: name}, host); err != nil {
		t.Fatalf("get host %s: %v", name, err)
	}
	return consumerName(host)
}

func TestResolveHost_ClaimsFirstEligibleHostByName(t *testing.T) {
	worker := map[string]string{"role": "worker"}
	claimed := claimHostFixture("host-a", true, worker)
	claimed.Spec.ConsumerRef = &infrav1.LibvirtMachineReference{Name: "other"}
	small := claimHostFixture("host-b", true, worker)
	small.Status.Capacity.AvailableMemoryMB = 1024
	r, c := claimReconciler(t,
		claimed,
		small,
		claimHostFixture("host-c", false, worker),                                    // not ready
		claimHostFixture("host-d", true, map[string]string{"role": "control-plane"}), // no match
		claimHostFixture("host-f", true, worker),
		claimHostFixture("host-e", true, worker),
	)
	lm := selectorMachine("m1")
	lm.Spec.Domain.MemoryMB = 4096

	host, err := r.resolveHost(context.Background(), lm, &clusterv1.Machine{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if host == nil || host.Name != "host-e" {
		t.Fatalf("got host %v, want host-e (the first free, ready, matching host with room)", host)
	}
	if got := hostConsumer(t, c, "host-e"); got != "m1" {
		t.Errorf("host-e consumer is %q, want m1", got)
	}
	if lm.Status.HostRef == nil || lm.Status.HostRef.Name != "host-e" {
		t.Errorf("status.hostRef = %v, want host-e", lm.Status.HostRef)
	}
	if !apimeta.IsStatusConditionTrue(lm.Status.Conditions, infrav1.HostAssignedCondition) {
		t.Error("HostAssigned is not True")
	}
}

// KubeadmControlPlane spreads machines by setting a failure domain, which is
// a host name; only that host may be claimed.
func TestResolveHost_HonoursFailureDomain(t *testing.T) {
	worker := map[string]string{"role": "worker"}
	r, _ := claimReconciler(t, claimHostFixture("host-a", true, worker), claimHostFixture("host-b", true, worker))
	machine := &clusterv1.Machine{Spec: clusterv1.MachineSpec{FailureDomain: "host-b"}}

	host, err := r.resolveHost(context.Background(), selectorMachine("m1"), machine)
	if err != nil || host == nil || host.Name != "host-b" {
		t.Fatalf("got host %v, err %v; want host-b", host, err)
	}
}

func TestResolveHost_NoHostAvailable(t *testing.T) {
	taken := claimHostFixture("host-a", true, map[string]string{"role": "worker"})
	taken.Spec.ConsumerRef = &infrav1.LibvirtMachineReference{Name: "other"}
	r, _ := claimReconciler(t, taken)
	lm := selectorMachine("m1")

	host, err := r.resolveHost(context.Background(), lm, &clusterv1.Machine{})
	if err != nil || host != nil {
		t.Fatalf("got host %v, err %v; want neither", host, err)
	}
	cond := apimeta.FindStatusCondition(lm.Status.Conditions, infrav1.HostAssignedCondition)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != infrav1.ReasonNoHostAvailable {
		t.Errorf("HostAssigned = %+v, want False/%s", cond, infrav1.ReasonNoHostAvailable)
	}
}

// The claim is the record: a machine whose status was lost (clusterctl move
// does not carry status) finds its host again rather than claiming another.
func TestResolveHost_RebuildsFromClaim(t *testing.T) {
	worker := map[string]string{"role": "worker"}
	mine := claimHostFixture("host-b", true, worker)
	mine.Spec.ConsumerRef = &infrav1.LibvirtMachineReference{Name: "m1"}
	r, c := claimReconciler(t, claimHostFixture("host-a", true, worker), mine)

	host, err := r.resolveHost(context.Background(), selectorMachine("m1"), &clusterv1.Machine{})
	if err != nil || host == nil || host.Name != "host-b" {
		t.Fatalf("got host %v, err %v; want host-b", host, err)
	}
	if got := hostConsumer(t, c, "host-a"); got != "" {
		t.Errorf("host-a was claimed by %q; the machine took a second host", got)
	}
}

func TestResolveHost_PinnedHost(t *testing.T) {
	pinned := func(name string) *infrav1.LibvirtMachine {
		return &infrav1.LibvirtMachine{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: claimNamespace},
			Spec:       infrav1.LibvirtMachineSpec{HostRef: &infrav1.LibvirtHostReference{Name: "host-a"}},
		}
	}
	r, c := claimReconciler(t, claimHostFixture("host-a", true, nil))

	host, err := r.resolveHost(context.Background(), pinned("m1"), &clusterv1.Machine{})
	if err != nil || host == nil {
		t.Fatalf("got host %v, err %v; want host-a", host, err)
	}
	if got := hostConsumer(t, c, "host-a"); got != "m1" {
		t.Errorf("host-a consumer is %q, want m1: pinned machines claim their host too", got)
	}

	other := pinned("m2")
	host, err = r.resolveHost(context.Background(), other, &clusterv1.Machine{})
	if err != nil || host != nil {
		t.Fatalf("got host %v, err %v; want neither for a host another machine holds", host, err)
	}
	cond := apimeta.FindStatusCondition(other.Status.Conditions, infrav1.HostAssignedCondition)
	if cond == nil || cond.Reason != infrav1.ReasonHostInUse {
		t.Errorf("HostAssigned = %+v, want reason %s", cond, infrav1.ReasonHostInUse)
	}
}

// A stale copy loses the race without an error, and the claim is not made.
func TestClaimHost_StaleCopyLosesRace(t *testing.T) {
	r, c := claimReconciler(t, claimHostFixture("host-a", true, nil))
	stale := &infrav1.LibvirtHost{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: claimNamespace, Name: "host-a"}, stale); err != nil {
		t.Fatal(err)
	}
	fresh := stale.DeepCopy()
	if claimed, err := r.claimHost(context.Background(), fresh, selectorMachine("winner")); err != nil || !claimed {
		t.Fatalf("first claim: claimed=%v err=%v", claimed, err)
	}

	claimed, err := r.claimHost(context.Background(), stale, selectorMachine("loser"))
	if err != nil || claimed {
		t.Fatalf("stale claim: claimed=%v err=%v; want a lost race", claimed, err)
	}
	if got := hostConsumer(t, c, "host-a"); got != "winner" {
		t.Errorf("host-a consumer is %q, want winner", got)
	}
}

func TestReleaseHost_OnlyOwnClaim(t *testing.T) {
	held := claimHostFixture("host-a", true, nil)
	held.Spec.ConsumerRef = &infrav1.LibvirtMachineReference{Name: "owner"}
	r, c := claimReconciler(t, held)
	host := &infrav1.LibvirtHost{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: claimNamespace, Name: "host-a"}, host); err != nil {
		t.Fatal(err)
	}

	if err := r.releaseHost(context.Background(), host, selectorMachine("someone-else")); err != nil {
		t.Fatal(err)
	}
	if got := hostConsumer(t, c, "host-a"); got != "owner" {
		t.Fatalf("another machine released the claim; consumer is %q", got)
	}
	if err := r.releaseHost(context.Background(), host, selectorMachine("owner")); err != nil {
		t.Fatal(err)
	}
	if got := hostConsumer(t, c, "host-a"); got != "" {
		t.Errorf("consumer is %q after release, want none", got)
	}
}
