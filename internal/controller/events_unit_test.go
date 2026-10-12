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
	"strings"
	"testing"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// drain returns every event currently buffered in a fake recorder.
func drain(rec *record.FakeRecorder) []string {
	var got []string
	for {
		select {
		case e := <-rec.Events:
			got = append(got, e)
		default:
			return got
		}
	}
}

func newHost() *infrav1.LibvirtHost {
	return &infrav1.LibvirtHost{
		ObjectMeta: metav1.ObjectMeta{Name: "host-1", Namespace: "default"},
	}
}

// A host is health-checked on a timer for as long as machines reference it, so
// a host that stays in one state must not emit an event per reconcile.
func TestHostEventsOnlyOnTransition(t *testing.T) {
	rec := record.NewFakeRecorder(10)
	r := &LibvirtHostReconciler{Recorder: rec}
	host := newHost()

	r.markUnreachable(host, infrav1.ReasonConnectionFailed, "ssh refused")
	if got := drain(rec); len(got) != 1 {
		t.Fatalf("first failure should emit exactly one event, got %d: %v", len(got), got)
	}

	// Same status and reason twice more: still down, nothing new to say.
	r.markUnreachable(host, infrav1.ReasonConnectionFailed, "ssh refused")
	r.markUnreachable(host, infrav1.ReasonConnectionFailed, "ssh refused again")
	if got := drain(rec); len(got) != 0 {
		t.Fatalf("unchanged state should emit nothing, got %v", got)
	}

	// A different reason is a different problem, so it is worth reporting.
	r.markUnreachable(host, infrav1.ReasonHypervisorUnavailable, "kvm missing")
	got := drain(rec)
	if len(got) != 1 {
		t.Fatalf("reason change should emit one event, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "Warning") || !strings.Contains(got[0], infrav1.ReasonHypervisorUnavailable) {
		t.Errorf("expected a Warning naming the new reason, got %q", got[0])
	}

	// Recovery.
	r.markReachable(host, "all good")
	got = drain(rec)
	if len(got) != 1 {
		t.Fatalf("recovery should emit one event, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "Normal") {
		t.Errorf("recovery should be a Normal event, got %q", got[0])
	}
	if !host.Status.Ready {
		t.Error("markReachable should set Status.Ready")
	}
}

// The condition must still be written even when no event is emitted, so
// status does not depend on the recorder.
func TestHostConditionWrittenWithoutRecorder(t *testing.T) {
	r := &LibvirtHostReconciler{} // deliberately no Recorder
	host := newHost()

	r.markUnreachable(host, infrav1.ReasonConnectionFailed, "ssh refused")

	cond := findCondition(host.Status.Conditions, infrav1.HostReachableCondition)
	if cond == nil {
		t.Fatal("condition was not set")
	}
	if cond.Status != metav1.ConditionFalse || cond.Reason != infrav1.ReasonConnectionFailed {
		t.Errorf("unexpected condition: %+v", cond)
	}
	if host.Status.Ready {
		t.Error("markUnreachable should clear Status.Ready")
	}
}

// A reconciler built without a Recorder is exactly how the unit tests
// construct one; recording must degrade to a no-op rather than panic.
func TestRecordEventToleratesNilRecorder(t *testing.T) {
	recordEvent(nil, newHost(), "Normal", "Reason", "message")
	recordEventf(nil, newHost(), "Normal", "Reason", "message %d", 1)

	// A nil object is equally not worth panicking over.
	rec := record.NewFakeRecorder(1)
	recordEvent(rec, nil, "Normal", "Reason", "message")
	if got := drain(rec); len(got) != 0 {
		t.Errorf("nil object should record nothing, got %v", got)
	}
}

func findCondition(conds []metav1.Condition, condType string) *metav1.Condition {
	for i := range conds {
		if conds[i].Type == condType {
			return &conds[i]
		}
	}
	return nil
}

// The cluster reconciler runs on every Cluster change, so a cluster that is
// simply still ready must not emit an event each time.
func TestClusterEventOnReadinessTransitionOnly(t *testing.T) {
	sch := runtime.NewScheme()
	if err := infrav1.AddToScheme(sch); err != nil {
		t.Fatal(err)
	}
	if err := clusterv1.AddToScheme(sch); err != nil {
		t.Fatal(err)
	}
	owner := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "owner", Namespace: "default"},
	}
	obj := &infrav1.LibvirtCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: "infra", Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: clusterv1.GroupVersion.String(), Kind: "Cluster", Name: owner.Name,
			}},
		},
		Spec: infrav1.LibvirtClusterSpec{
			ControlPlaneEndpoint: clusterv1.APIEndpoint{Host: "127.0.0.1", Port: 6443},
		},
	}
	owner.Spec.InfrastructureRef = &corev1.ObjectReference{
		APIVersion: infrav1.GroupVersion.String(), Kind: "LibvirtCluster", Name: obj.Name,
	}
	c := fake.NewClientBuilder().WithScheme(sch).
		WithStatusSubresource(obj).WithObjects(owner, obj).Build()
	rec := record.NewFakeRecorder(10)
	r := &LibvirtClusterReconciler{Client: c, Scheme: sch, Recorder: rec}
	key := types.NamespacedName{Namespace: obj.Namespace, Name: obj.Name}

	for range 3 {
		if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
	}

	got := drain(rec)
	if len(got) != 1 {
		t.Fatalf("three reconciles of a steady cluster should emit one event, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "Normal") || !strings.Contains(got[0], infrav1.ReasonClusterReady) {
		t.Errorf("expected a Normal event naming %s, got %q", infrav1.ReasonClusterReady, got[0])
	}
}
