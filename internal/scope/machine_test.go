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
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
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

func TestHostname(t *testing.T) {
	s := newTestScope()
	if got := s.Hostname(); got != s.DomainName() {
		t.Errorf("Hostname() = %q, want the domain name when it fits", got)
	}

	// CAPI's generated names make long domain names common.
	long := "quick-start-8qwqi3-quick-start-puyh8y-quick-start-puyh8y-ck9bm-8flmb"
	got := HostnameFor(long)
	if len(got) > maxHostnameLength {
		t.Errorf("HostnameFor(%q) = %q, %d characters, want at most %d", long, got, len(got), maxHostnameLength)
	}
	if !strings.HasPrefix(got, long[:40]) {
		t.Errorf("HostnameFor(%q) = %q, want it to start with the domain name", long, got)
	}
	if other := HostnameFor(long[:len(long)-1] + "x"); other == got {
		t.Errorf("names differing after the cut both map to %q", got)
	}
	// A cut that lands on a hyphen must not leave a double hyphen.
	if got := HostnameFor(strings.Repeat("a", 53) + "-" + strings.Repeat("b", 20)); strings.Contains(got, "--") {
		t.Errorf("hostnameFor left a double hyphen: %q", got)
	}
}

func TestMACAddress(t *testing.T) {
	s := newTestScope()
	derived := s.MACAddress()
	if !strings.HasPrefix(derived, "52:54:00:") || len(derived) != len("52:54:00:00:00:00") {
		t.Errorf("MACAddress() = %q, want a 52:54:00 address", derived)
	}
	if again := s.MACAddress(); again != derived {
		t.Errorf("MACAddress() changed between calls: %q then %q", derived, again)
	}
	s.LibvirtMachine.Spec.Network.MACAddress = "52:54:00:12:34:56"
	if got := s.MACAddress(); got != "52:54:00:12:34:56" {
		t.Errorf("MACAddress() = %q, want the one set in the spec", got)
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
		if addr.Type != infrav1.MachineInternalIP {
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
