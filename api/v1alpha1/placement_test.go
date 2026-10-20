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

package v1alpha1

import (
	"reflect"
	"testing"
)

func TestEffectiveNetwork(t *testing.T) {
	host := &MachineNetworkSpec{
		Type:      NetworkTypeBridge,
		Name:      "br0",
		Addresses: []string{"10.0.0.51/24"},
		Gateway:   "10.0.0.1",
		DNS:       &DNSSpec{Nameservers: []string{"10.0.0.2"}},
	}

	// Unset machine fields come from the host; set ones win.
	machine := NetworkSpec{Model: "virtio", DNS: &DNSSpec{Nameservers: []string{"1.1.1.1"}}}
	got := EffectiveNetwork(machine, host)
	want := NetworkSpec{
		Type:      NetworkTypeBridge,
		Name:      "br0",
		Model:     "virtio",
		Addresses: []string{"10.0.0.51/24"},
		Gateway:   "10.0.0.1",
		DNS:       &DNSSpec{Nameservers: []string{"1.1.1.1"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EffectiveNetwork() = %+v, want %+v", got, want)
	}

	// The result shares nothing with the host.
	got.Addresses[0] = "changed"
	if host.Addresses[0] != "10.0.0.51/24" {
		t.Error("EffectiveNetwork returned the host's addresses slice")
	}

	if got := EffectiveNetwork(machine, nil); !reflect.DeepEqual(got, machine) {
		t.Errorf("EffectiveNetwork(machine, nil) = %+v, want the machine's own", got)
	}
}

func TestAssignedHostName(t *testing.T) {
	m := &LibvirtMachine{}
	if got := m.AssignedHostName(); got != "" {
		t.Errorf("unassigned machine: got %q", got)
	}
	m.Status.HostRef = &LibvirtHostReference{Name: "claimed"}
	if got := m.AssignedHostName(); got != "claimed" {
		t.Errorf("got %q, want the claimed host", got)
	}
	m.Spec.HostRef = &LibvirtHostReference{Name: "pinned"}
	if got := m.AssignedHostName(); got != "pinned" {
		t.Errorf("got %q, want the pinned host", got)
	}
}
