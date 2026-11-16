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
	"errors"
	"testing"

	gossh "golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

// A machine created while its host was not Ready reports HostReachable=False
// with HostNotReady. Once the host is Ready, the next reconcile must replace
// that, whatever the connection's outcome, rather than leave it stale.
func TestReconcileNormal_ReplacesHostNotReadyOnceTheHostIsReady(t *testing.T) {
	host := &infrav1.LibvirtHost{
		ObjectMeta: metav1.ObjectMeta{Name: "host1", Namespace: "default"},
		Spec: infrav1.LibvirtHostSpec{
			URI:       "qemu+ssh://caplv@host/system",
			SecretRef: &infrav1.SecretReference{Name: "ssh-key"},
		},
		Status: infrav1.LibvirtHostStatus{Ready: true},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "ssh-key", Namespace: "default"},
		Data:       map[string][]byte{"ssh-privatekey": []byte("fake-key")},
	}
	machine := &infrav1.LibvirtMachine{ObjectMeta: metav1.ObjectMeta{Name: "m1", Namespace: "default"}}
	apimeta.SetStatusCondition(&machine.Status.Conditions, metav1.Condition{
		Type:    infrav1.HostReachableForMachineCondition,
		Status:  metav1.ConditionFalse,
		Reason:  infrav1.ReasonHostNotReady,
		Message: "LibvirtHost is not ready",
	})

	r := &LibvirtMachineReconciler{
		Client: fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(secret).Build(),
		SSHClientFactory: func(context.Context, *infrav1.LibvirtHost, *corev1.Secret) (*gossh.Client, error) {
			return nil, errors.New("connection refused")
		},
	}
	if _, err := r.reconcileNormal(context.Background(), machine, nil, nil, nil, host); err == nil {
		t.Fatal("expected the failed connection to be returned")
	}

	got := apimeta.FindStatusCondition(machine.Status.Conditions, infrav1.HostReachableForMachineCondition)
	if got == nil || got.Reason != infrav1.ReasonConnectionFailed {
		t.Fatalf("HostReachable = %+v, want reason %s", got, infrav1.ReasonConnectionFailed)
	}
}

func TestSetHostReachableCondition(t *testing.T) {
	machine := &infrav1.LibvirtMachine{ObjectMeta: metav1.ObjectMeta{Generation: 3}}
	apimeta.SetStatusCondition(&machine.Status.Conditions, metav1.Condition{
		Type:    infrav1.HostReachableForMachineCondition,
		Status:  metav1.ConditionFalse,
		Reason:  infrav1.ReasonHostNotReady,
		Message: "LibvirtHost is not ready",
	})

	setHostReachableCondition(machine, nil)
	got := apimeta.FindStatusCondition(machine.Status.Conditions, infrav1.HostReachableForMachineCondition)
	if got.Status != metav1.ConditionTrue || got.Reason != infrav1.ReasonConnectionSucceeded ||
		got.Message != "" || got.ObservedGeneration != 3 {
		t.Errorf("after a connection, HostReachable = %+v, want True/%s with no message",
			got, infrav1.ReasonConnectionSucceeded)
	}

	setHostReachableCondition(machine, errors.New("dial tcp: i/o timeout"))
	got = apimeta.FindStatusCondition(machine.Status.Conditions, infrav1.HostReachableForMachineCondition)
	if got.Status != metav1.ConditionFalse || got.Reason != infrav1.ReasonConnectionFailed ||
		got.Message != "dial tcp: i/o timeout" {
		t.Errorf("after a failed connection, HostReachable = %+v, want False/%s with the error",
			got, infrav1.ReasonConnectionFailed)
	}
}
