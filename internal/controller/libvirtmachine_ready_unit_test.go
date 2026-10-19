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
	"testing"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/ptr"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

func cond(t string, status metav1.ConditionStatus, reason, message string) metav1.Condition {
	return metav1.Condition{Type: t, Status: status, Reason: reason, Message: message}
}

func TestSetMachineReadyCondition(t *testing.T) {
	now := metav1.Now()
	for _, tc := range []struct {
		name          string
		deleting      bool
		failureReason *string
		failureMsg    *string
		conditions    []metav1.Condition
		wantStatus    metav1.ConditionStatus
		wantReason    string
		wantMessage   string
	}{
		{
			name:       "nothing reported yet",
			wantStatus: metav1.ConditionFalse, wantReason: infrav1.ReasonProvisioning,
			wantMessage: "LibvirtMachine is being provisioned",
		},
		{
			name: "waiting for bootstrap data",
			conditions: []metav1.Condition{
				cond(infrav1.BootstrapDataReadyCondition, metav1.ConditionFalse, infrav1.ReasonBootstrapDataNotReady, "waiting"),
			},
			wantStatus: metav1.ConditionFalse, wantReason: infrav1.ReasonBootstrapDataNotReady, wantMessage: "waiting",
		},
		{
			name: "host blocker reported before bootstrap blocker",
			conditions: []metav1.Condition{
				cond(infrav1.BootstrapDataReadyCondition, metav1.ConditionFalse, infrav1.ReasonBootstrapDataNotReady, "waiting"),
				cond(infrav1.HostReachableForMachineCondition, metav1.ConditionFalse, infrav1.ReasonHostNotReady, "host down"),
			},
			wantStatus: metav1.ConditionFalse, wantReason: infrav1.ReasonHostNotReady, wantMessage: "host down",
		},
		{
			name: "provisioned",
			conditions: []metav1.Condition{
				cond(infrav1.BootstrapDataReadyCondition, metav1.ConditionTrue, infrav1.ReasonBootstrapDataReady, ""),
				cond(infrav1.InfrastructureReadyCondition, metav1.ConditionTrue, infrav1.ReasonProvisioned, "ready"),
			},
			wantStatus: metav1.ConditionTrue, wantReason: infrav1.ReasonProvisioned, wantMessage: "ready",
		},
		{
			name:          "terminal failure",
			failureReason: ptr.To(infrav1.ReasonStoragePoolNotFound),
			failureMsg:    ptr.To("pool missing"),
			conditions: []metav1.Condition{
				cond(infrav1.InfrastructureReadyCondition, metav1.ConditionTrue, infrav1.ReasonProvisioned, "ready"),
			},
			wantStatus: metav1.ConditionFalse, wantReason: infrav1.ReasonStoragePoolNotFound, wantMessage: "pool missing",
		},
		{
			name:     "deleting",
			deleting: true,
			conditions: []metav1.Condition{
				cond(infrav1.InfrastructureReadyCondition, metav1.ConditionTrue, infrav1.ReasonProvisioned, "ready"),
			},
			wantStatus: metav1.ConditionFalse, wantReason: infrav1.ReasonDeleting,
			wantMessage: "LibvirtMachine is being deleted",
		},
		{
			name:     "deleting with cleanup stalled",
			deleting: true,
			conditions: []metav1.Condition{
				cond(infrav1.CleanupStalledCondition, metav1.ConditionTrue, infrav1.ReasonCleanupStalled, "host unreachable"),
			},
			wantStatus: metav1.ConditionFalse, wantReason: infrav1.ReasonDeleting, wantMessage: "host unreachable",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lm := &infrav1.LibvirtMachine{ObjectMeta: metav1.ObjectMeta{Generation: 4}}
			if tc.deleting {
				lm.DeletionTimestamp = &now
			}
			lm.Status.FailureReason = tc.failureReason
			lm.Status.FailureMessage = tc.failureMsg
			lm.Status.Conditions = tc.conditions

			setMachineReadyCondition(lm)

			got := apimeta.FindStatusCondition(lm.Status.Conditions, infrav1.ReadyCondition)
			if got == nil {
				t.Fatal("Ready condition not set")
			}
			if got.Status != tc.wantStatus || got.Reason != tc.wantReason || got.Message != tc.wantMessage {
				t.Errorf("Ready = %s/%s/%q, want %s/%s/%q",
					got.Status, got.Reason, got.Message, tc.wantStatus, tc.wantReason, tc.wantMessage)
			}
			if got.ObservedGeneration != 4 {
				t.Errorf("observedGeneration = %d, want 4", got.ObservedGeneration)
			}
		})
	}
}

// Every terminal error must reach the conditions, the only failure signal
// CAPI's v1beta2 contract reads, not just status.failureReason.
func TestSetTerminalErrorSetsConditions(t *testing.T) {
	r := &LibvirtMachineReconciler{Recorder: &events.FakeRecorder{Events: make(chan string, 1)}}
	lm := &infrav1.LibvirtMachine{}
	lm.Status.Conditions = []metav1.Condition{
		cond(infrav1.InfrastructureReadyCondition, metav1.ConditionTrue, infrav1.ReasonProvisioned, "ready"),
	}

	r.setTerminalError(lm, infrav1.ReasonSpecMismatch, "domain differs")
	setMachineReadyCondition(lm)

	for _, t2 := range []string{infrav1.InfrastructureReadyCondition, infrav1.ReadyCondition} {
		c := apimeta.FindStatusCondition(lm.Status.Conditions, t2)
		if c == nil || c.Status != metav1.ConditionFalse || c.Reason != infrav1.ReasonSpecMismatch || c.Message != "domain differs" {
			t.Errorf("%s = %+v, want False/%s", t2, c, infrav1.ReasonSpecMismatch)
		}
	}
}
