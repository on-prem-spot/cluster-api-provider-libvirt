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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

func TestLibvirtMachineForNode_HonoursWatchFilter(t *testing.T) {
	s := runtime.NewScheme()
	if err := infrav1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := clusterv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	machine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{Name: "m", Namespace: "default",
			Labels: map[string]string{clusterv1.ClusterNameLabel: "spot"}},
		Spec: clusterv1.MachineSpec{ClusterName: "spot", InfrastructureRef: corev1.ObjectReference{
			APIVersion: infrav1.GroupVersion.String(), Kind: "LibvirtMachine", Namespace: "default", Name: "worker01"}},
	}
	lm := &infrav1.LibvirtMachine{ObjectMeta: metav1.ObjectMeta{Name: "worker01", Namespace: "default",
		Labels: map[string]string{clusterv1.WatchLabel: "tenant-a"}}}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(machine, lm).Build()

	for _, tc := range []struct {
		filter string
		found  bool
	}{
		{filter: "", found: true},
		{filter: "tenant-a", found: true},
		{filter: "tenant-b", found: false},
	} {
		r := &CSRApproverReconciler{Client: c, Scheme: s, WatchFilterValue: tc.filter}
		got := r.libvirtMachineForNode(context.Background(), testNodeName)
		if (got != nil) != tc.found {
			t.Errorf("filter %q: found=%v, want %v", tc.filter, got != nil, tc.found)
		}
	}
}
