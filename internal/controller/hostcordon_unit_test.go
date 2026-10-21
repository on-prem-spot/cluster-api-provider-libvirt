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

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
	"github.com/on-prem-spot/cluster-api-provider-libvirt/internal/libvirt"
)

// A selector never picks a cordoned host.
func TestResolveHost_SkipsCordonedHosts(t *testing.T) {
	worker := map[string]string{"role": "worker"}
	cordoned := claimHostFixture("host-a", true, worker)
	cordoned.Spec.Unschedulable = true
	r, c := claimReconciler(t, cordoned, claimHostFixture(hostB, true, worker))

	host, err := r.resolveHost(context.Background(), selectorMachine("m1"), &clusterv1.Machine{})
	if err != nil || host == nil || host.Name != hostB {
		t.Fatalf("got host %v, err %v; want host-b", host, err)
	}
	if got := hostConsumer(t, c, "host-a"); got != "" {
		t.Errorf("cordoned host-a was claimed by %q", got)
	}
}

// A cordoned host keeps the machine that holds it and takes no other.
func TestResolveHost_PinnedToCordonedHost(t *testing.T) {
	pinned := func(name string) *infrav1.LibvirtMachine {
		return &infrav1.LibvirtMachine{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: claimNamespace},
			Spec:       infrav1.LibvirtMachineSpec{HostRef: &infrav1.LibvirtHostReference{Name: "host-a"}},
		}
	}
	held := claimHostFixture("host-a", true, nil)
	held.Spec.Unschedulable = true
	held.Spec.ConsumerRef = &infrav1.LibvirtMachineReference{Name: "m1"}
	r, _ := claimReconciler(t, held)

	if host, err := r.resolveHost(context.Background(), pinned("m1"), &clusterv1.Machine{}); err != nil || host == nil {
		t.Fatalf("got host %v, err %v; want the machine to keep its cordoned host", host, err)
	}

	free := claimHostFixture("host-a", true, nil)
	free.Spec.Unschedulable = true
	r, c := claimReconciler(t, free)
	waiting := pinned("m2")
	host, err := r.resolveHost(context.Background(), waiting, &clusterv1.Machine{})
	if err != nil || host != nil {
		t.Fatalf("got host %v, err %v; want none for a cordoned host", host, err)
	}
	if got := hostConsumer(t, c, "host-a"); got != "" {
		t.Errorf("cordoned host-a was claimed by %q", got)
	}
	cond := apimeta.FindStatusCondition(waiting.Status.Conditions, infrav1.HostAssignedCondition)
	if cond == nil || cond.Reason != infrav1.ReasonHostCordoned {
		t.Errorf("HostAssigned = %+v, want reason %s", cond, infrav1.ReasonHostCordoned)
	}
}

func TestWaitWhileCordoned(t *testing.T) {
	for _, tc := range []struct {
		name       string
		cordoned   bool
		domain     string // "" for no domain, else its state
		wantWait   bool
		wantReason string
	}{
		{name: "host not cordoned", domain: "", wantWait: false},
		{name: "running VM keeps running", cordoned: true, domain: "running", wantWait: false},
		// A VM destroyed to free the host is not started again.
		{name: "stopped VM stays stopped", cordoned: true, domain: "shutoff", wantWait: true,
			wantReason: infrav1.ReasonHostCordoned},
		{name: "no VM is created", cordoned: true, domain: "", wantWait: true,
			wantReason: infrav1.ReasonHostCordoned},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &libvirt.MockClient{
				DomainExistsFn: func(context.Context, string) (bool, error) { return tc.domain != "", nil },
				GetDomainFn: func(_ context.Context, name string) (*libvirt.DomainInfo, error) {
					return &libvirt.DomainInfo{Name: name, State: tc.domain}, nil
				},
			}
			rc := &reconcileCtx{
				libvirtMachine: &infrav1.LibvirtMachine{},
				libvirtHost: &infrav1.LibvirtHost{ObjectMeta: metav1.ObjectMeta{Name: "host-a"},
					Spec: infrav1.LibvirtHostSpec{Unschedulable: tc.cordoned}},
				libvirtClient: client,
				domainName:    "ns-cl-m1",
			}

			wait, err := (&LibvirtMachineReconciler{}).waitWhileCordoned(context.Background(), rc)
			if err != nil {
				t.Fatalf("waitWhileCordoned: %v", err)
			}
			if wait != tc.wantWait {
				t.Errorf("wait = %v, want %v", wait, tc.wantWait)
			}
			cond := apimeta.FindStatusCondition(rc.libvirtMachine.Status.Conditions, infrav1.InfrastructureReadyCondition)
			if tc.wantReason == "" {
				if cond != nil {
					t.Errorf("unexpected InfrastructureReady %+v", cond)
				}
			} else if cond == nil || cond.Reason != tc.wantReason || cond.Status != metav1.ConditionFalse {
				t.Errorf("InfrastructureReady = %+v, want False/%s", cond, tc.wantReason)
			}
		})
	}
}
