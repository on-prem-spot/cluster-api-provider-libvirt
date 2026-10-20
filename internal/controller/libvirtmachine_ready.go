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
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

// provisioningBlockers are the conditions that can hold a machine back before
// InfrastructureReady is first set, in the order reconcileNormal checks them.
var provisioningBlockers = []string{
	infrav1.HostReachableForMachineCondition,
	infrav1.BootstrapDataReadyCondition,
	infrav1.BaseImageStagedCondition,
}

// setMachineReadyCondition derives the Ready condition CAPI mirrors into the
// Machine's InfrastructureReady condition. The v1beta2 contract expects it to
// describe the machine through provisioning, a terminal failure and deletion,
// so it is recomputed from the other conditions on every reconcile.
func setMachineReadyCondition(lm *infrav1.LibvirtMachine) {
	ready := metav1.Condition{
		Type:               infrav1.ReadyCondition,
		Status:             metav1.ConditionFalse,
		Reason:             infrav1.ReasonProvisioning,
		Message:            "LibvirtMachine is being provisioned",
		ObservedGeneration: lm.Generation,
	}

	switch {
	case !lm.DeletionTimestamp.IsZero():
		ready.Reason = infrav1.ReasonDeleting
		ready.Message = "LibvirtMachine is being deleted"
		if stalled := apimeta.FindStatusCondition(lm.Status.Conditions, infrav1.CleanupStalledCondition); stalled != nil &&
			stalled.Status == metav1.ConditionTrue {
			ready.Message = stalled.Message
		}
	case lm.Status.FailureReason != nil:
		ready.Reason = *lm.Status.FailureReason
		ready.Message = ptr.Deref(lm.Status.FailureMessage, "")
	default:
		if infra := apimeta.FindStatusCondition(lm.Status.Conditions, infrav1.InfrastructureReadyCondition); infra != nil {
			ready.Status, ready.Reason, ready.Message = infra.Status, infra.Reason, infra.Message
			break
		}
		for _, t := range provisioningBlockers {
			if c := apimeta.FindStatusCondition(lm.Status.Conditions, t); c != nil && c.Status != metav1.ConditionTrue {
				ready.Reason, ready.Message = c.Reason, c.Message
				break
			}
		}
	}
	// CAPI v1beta2 keeps healthy conditions silent: a True condition with a
	// message is summarised into the Machine's and Cluster's conditions as if
	// it needed attention.
	if ready.Status == metav1.ConditionTrue {
		ready.Message = ""
	}
	apimeta.SetStatusCondition(&lm.Status.Conditions, ready)
}
