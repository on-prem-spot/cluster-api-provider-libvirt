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
	"sort"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

// hostClaimRequeueInterval is how soon a machine waiting for a host, or one
// whose claim lost a race, looks again.
const hostClaimRequeueInterval = 30 * time.Second

// Host claims (ADR 0006). A LibvirtHost runs one VM, recorded by setting the
// host's spec.consumerRef to the LibvirtMachine. The claim is made with an
// update carrying the host's resourceVersion, so of two machines racing for
// one host only one succeeds. Lookups of existing claims read the API server
// directly rather than the cache: a claim the cache has not seen yet would
// otherwise let a machine claim a second host.

// resolveHost returns the host the machine runs on, claiming one when the
// machine has none yet. It returns a nil host, with the HostAssigned
// condition saying why, when the machine has to wait: no host is free, or its
// pinned host belongs to another machine. A machine being deleted that never
// got a host also yields nil.
func (r *LibvirtMachineReconciler) resolveHost(
	ctx context.Context,
	lm *infrav1.LibvirtMachine,
	machine *clusterv1.Machine,
) (*infrav1.LibvirtHost, error) {
	deleting := !lm.DeletionTimestamp.IsZero()

	name := lm.AssignedHostName()
	if name == "" {
		claimed, err := r.findClaimedHost(ctx, lm)
		if err != nil {
			return nil, err
		}
		name = claimed
	}
	if name == "" {
		if deleting {
			return nil, nil
		}
		host, err := r.claimHostFromSelector(ctx, lm, machine)
		if err != nil || host == nil {
			return nil, err
		}
		r.recordHost(lm, host.Name)
		return host, nil
	}

	host := &infrav1.LibvirtHost{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: lm.Namespace, Name: name}, host); err != nil {
		return nil, fmt.Errorf("failed to get LibvirtHost: %w", err)
	}
	if !deleting {
		// A cordoned host keeps the machine that holds it, but takes no other.
		if host.Spec.Unschedulable && consumerName(host) != lm.Name {
			setHostAssignedCondition(lm, metav1.ConditionFalse, infrav1.ReasonHostCordoned,
				fmt.Sprintf("LibvirtHost %s is cordoned (spec.unschedulable)", host.Name))
			return nil, nil
		}
		claimed, err := r.claimHost(ctx, host, lm)
		if err != nil {
			return nil, err
		}
		if !claimed {
			// No consumer left on the host means the update lost a race
			// (or read a stale copy); the next reconcile tries again.
			if other := consumerName(host); other != "" {
				setHostAssignedCondition(lm, metav1.ConditionFalse, infrav1.ReasonHostInUse,
					fmt.Sprintf("LibvirtHost %s is in use by LibvirtMachine %s", host.Name, other))
			}
			return nil, nil
		}
	}
	r.recordHost(lm, host.Name)
	return host, nil
}

// recordHost reports the host in status and on the HostAssigned condition.
func (r *LibvirtMachineReconciler) recordHost(lm *infrav1.LibvirtMachine, name string) {
	lm.Status.HostRef = &infrav1.LibvirtHostReference{Name: name}
	setHostAssignedCondition(lm, metav1.ConditionTrue, infrav1.ReasonHostAssigned, "")
}

// findClaimedHost returns the name of the host whose consumerRef names the
// machine, which rebuilds status.hostRef, e.g. after clusterctl move.
func (r *LibvirtMachineReconciler) findClaimedHost(ctx context.Context, lm *infrav1.LibvirtMachine) (string, error) {
	hosts := &infrav1.LibvirtHostList{}
	if err := r.hostReader().List(ctx, hosts, client.InNamespace(lm.Namespace)); err != nil {
		return "", fmt.Errorf("failed to list LibvirtHosts: %w", err)
	}
	for i := range hosts.Items {
		if consumerName(&hosts.Items[i]) == lm.Name {
			return hosts.Items[i].Name, nil
		}
	}
	return "", nil
}

// claimHostFromSelector claims the first eligible host, by name, matching
// spec.hostSelector. It returns nil, with HostAssigned False, when none is
// free.
func (r *LibvirtMachineReconciler) claimHostFromSelector(
	ctx context.Context,
	lm *infrav1.LibvirtMachine,
	machine *clusterv1.Machine,
) (*infrav1.LibvirtHost, error) {
	log := logf.FromContext(ctx)
	if lm.Spec.HostSelector == nil {
		return nil, fmt.Errorf("LibvirtMachine %s/%s sets neither spec.hostRef nor spec.hostSelector", lm.Namespace, lm.Name)
	}
	selector, err := metav1.LabelSelectorAsSelector(lm.Spec.HostSelector)
	if err != nil {
		return nil, fmt.Errorf("invalid spec.hostSelector: %w", err)
	}

	hosts := &infrav1.LibvirtHostList{}
	if err := r.hostReader().List(ctx, hosts, client.InNamespace(lm.Namespace),
		client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return nil, fmt.Errorf("failed to list LibvirtHosts: %w", err)
	}
	candidates := eligibleHosts(hosts.Items, lm, machine, selector)
	for i := range candidates {
		host := &candidates[i]
		claimed, err := r.claimHost(ctx, host, lm)
		if err != nil {
			return nil, err
		}
		if claimed {
			log.Info("Claimed LibvirtHost", "host", host.Name)
			return host, nil
		}
	}

	message := fmt.Sprintf("No free, ready LibvirtHost matches spec.hostSelector (%s)", selector)
	if machine != nil && machine.Spec.FailureDomain != "" {
		message += fmt.Sprintf(" in failure domain %s", machine.Spec.FailureDomain)
	}
	setHostAssignedCondition(lm, metav1.ConditionFalse, infrav1.ReasonNoHostAvailable, message)
	return nil, nil
}

// eligibleHosts returns, sorted by name, the hosts a machine may claim: ones
// matching the selector, ready, unclaimed, not cordoned, not being deleted, in the
// machine's failure domain when CAPI chose one, and with the capacity the
// machine asks for.
func eligibleHosts(
	hosts []infrav1.LibvirtHost,
	lm *infrav1.LibvirtMachine,
	machine *clusterv1.Machine,
	selector labels.Selector,
) []infrav1.LibvirtHost {
	var out []infrav1.LibvirtHost
	for i := range hosts {
		host := &hosts[i]
		switch {
		case !selector.Matches(labels.Set(host.Labels)),
			!host.DeletionTimestamp.IsZero(),
			host.Spec.ConsumerRef != nil,
			host.Spec.Unschedulable,
			!host.Status.Ready:
			continue
		case machine != nil && machine.Spec.FailureDomain != "" && machine.Spec.FailureDomain != host.Name:
			continue
		}
		if c := host.Status.Capacity; c != nil {
			if lm.Spec.Domain.VCPUs > c.AvailableVCPUs || lm.Spec.Domain.MemoryMB > c.AvailableMemoryMB {
				continue
			}
		}
		out = append(out, *host)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// claimHost sets the host's consumerRef to the machine unless another
// machine holds it. It reports whether the machine holds the claim; losing a
// race to another machine is not an error.
func (r *LibvirtMachineReconciler) claimHost(ctx context.Context, host *infrav1.LibvirtHost, lm *infrav1.LibvirtMachine) (bool, error) {
	if host.Spec.ConsumerRef != nil {
		return host.Spec.ConsumerRef.Name == lm.Name, nil
	}
	host.Spec.ConsumerRef = &infrav1.LibvirtMachineReference{Name: lm.Name}
	if err := r.Update(ctx, host); err != nil {
		host.Spec.ConsumerRef = nil
		if apierrors.IsConflict(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to claim LibvirtHost %s: %w", host.Name, err)
	}
	return true, nil
}

// releaseHost clears the host's consumerRef if it names the machine. Called
// once the machine's domain and artifacts are gone.
func (r *LibvirtMachineReconciler) releaseHost(ctx context.Context, host *infrav1.LibvirtHost, lm *infrav1.LibvirtMachine) error {
	if consumerName(host) != lm.Name {
		return nil
	}
	host.Spec.ConsumerRef = nil
	if err := r.Update(ctx, host); err != nil {
		return fmt.Errorf("failed to release LibvirtHost %s: %w", host.Name, err)
	}
	logf.FromContext(ctx).Info("Released LibvirtHost", "host", host.Name)
	return nil
}

// hostReader reads hosts for claim lookups, uncached when an APIReader is set.
func (r *LibvirtMachineReconciler) hostReader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
}

func consumerName(host *infrav1.LibvirtHost) string {
	if host.Spec.ConsumerRef == nil {
		return ""
	}
	return host.Spec.ConsumerRef.Name
}

func setHostAssignedCondition(lm *infrav1.LibvirtMachine, status metav1.ConditionStatus, reason, message string) {
	apimeta.SetStatusCondition(&lm.Status.Conditions, metav1.Condition{
		Type:               infrav1.HostAssignedCondition,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: lm.Generation,
	})
}
