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

package scope

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"

	infrav1 "github.com/atgreen/caplv/api/v1alpha1"
)

func newTestScope() *MachineScope {
	return &MachineScope{
		Cluster: &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		},
		Machine: &clusterv1.Machine{
			ObjectMeta: metav1.ObjectMeta{Name: "machine", Namespace: "ns"},
		},
		LibvirtCluster: &infrav1.LibvirtCluster{},
		LibvirtMachine: &infrav1.LibvirtMachine{
			ObjectMeta: metav1.ObjectMeta{Name: "machine", Namespace: "ns"},
			Spec: infrav1.LibvirtMachineSpec{
				Network: infrav1.NetworkSpec{
					Addresses: []string{"10.0.0.5/24", "192.168.1.10/16", "172.16.0.1"},
				},
			},
		},
		LibvirtHost: &infrav1.LibvirtHost{
			ObjectMeta: metav1.ObjectMeta{Name: "hostname"},
		},
	}
}

func TestArtifactBaseName(t *testing.T) {
	s := newTestScope()
	got := s.ArtifactBaseName()
	expected := "ns-cluster-machine"
	if got != expected {
		t.Errorf("ArtifactBaseName() = %q, want %q", got, expected)
	}
}

func TestDomainName(t *testing.T) {
	s := newTestScope()
	got := s.DomainName()
	expected := "ns-cluster-machine"
	if got != expected {
		t.Errorf("DomainName() = %q, want %q", got, expected)
	}
}

func TestRootDiskVolumeName(t *testing.T) {
	s := newTestScope()
	got := s.RootDiskVolumeName()
	if got != "ns-cluster-machine-root.qcow2" {
		t.Errorf("RootDiskVolumeName() = %q, want suffix -root.qcow2", got)
	}
}

func TestBootstrapISOName(t *testing.T) {
	s := newTestScope()
	got := s.BootstrapISOName()
	if got != "ns-cluster-machine-bootstrap.iso" {
		t.Errorf("BootstrapISOName() = %q, want suffix -bootstrap.iso", got)
	}
}

func TestProviderID(t *testing.T) {
	s := newTestScope()
	got := s.ProviderID()
	expected := "libvirt:///hostname/ns-cluster-machine"
	if got != expected {
		t.Errorf("ProviderID() = %q, want %q", got, expected)
	}
}

func TestGetAddresses_StripsCIDR(t *testing.T) {
	s := newTestScope()
	addrs := s.GetAddresses()

	if len(addrs) != 3 {
		t.Fatalf("expected 3 addresses, got %d", len(addrs))
	}

	expected := []string{"10.0.0.5", "192.168.1.10", "172.16.0.1"}
	for i, addr := range addrs {
		if addr.Address != expected[i] {
			t.Errorf("address[%d] = %q, want %q", i, addr.Address, expected[i])
		}
		if addr.Type != clusterv1.MachineInternalIP {
			t.Errorf("address[%d] type = %v, want MachineInternalIP", i, addr.Type)
		}
	}
}

// ArtifactBaseName joins namespace, cluster and machine with an unescaped '-',
// so distinct (cluster, machine) pairs can render to one name — and that name
// is what the reconciler looks the Node up by. This is why Node writes are
// gated on providerID rather than on the name alone. If delimiter escaping is
// ever added, this test should start failing and the ownership check can be
// revisited.
func TestArtifactBaseNameCollides(t *testing.T) {
	victim := newTestScope()
	victim.Cluster.Name = "prod"
	victim.LibvirtMachine.Name = "worker-1"

	other := newTestScope()
	other.Cluster.Name = "prod-worker"
	other.LibvirtMachine.Name = "1"

	if victim.ArtifactBaseName() != other.ArtifactBaseName() {
		t.Fatalf("expected a collision, got %q and %q",
			victim.ArtifactBaseName(), other.ArtifactBaseName())
	}
}
