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

package libvirt

import (
	"slices"
	"testing"
)

func TestDHCPReservationPlan(t *testing.T) {
	const mac, ip = "52:54:00:aa:bb:cc", "10.200.1.10"
	for _, tc := range []struct {
		name        string
		xml         string
		wantReserve bool
		wantRemove  []string
	}{
		{
			name:        "routed network without DHCP",
			xml:         `<network><name>caplv</name><ip address='10.200.1.1' netmask='255.255.255.0'/></network>`,
			wantReserve: true,
		},
		{
			// libvirt's DHCP gives the initramfs an address already, and every
			// update would rebuild the network's firewall rules.
			name: "network with its own DHCP range",
			xml: `<network><ip address='192.168.150.1' netmask='255.255.255.0'><dhcp>` +
				`<range start='192.168.150.100' end='192.168.150.200'/></dhcp></ip></network>`,
		},
		{
			name: "already reserved",
			xml: `<network><ip address='10.200.1.1' netmask='255.255.255.0'><dhcp>` +
				`<host mac='52:54:00:AA:BB:CC' name='w1' ip='10.200.1.10'/></dhcp></ip></network>`,
		},
		{
			name: "reserved for another address",
			xml: `<network><ip address='10.200.1.1' netmask='255.255.255.0'><dhcp>` +
				`<host mac='52:54:00:aa:bb:cc' name='w1' ip='10.200.1.11'/></dhcp></ip></network>`,
			wantReserve: true,
			wantRemove:  []string{"52:54:00:aa:bb:cc"},
		},
		{
			// A deleted VM's reservation, made under a MAC that did not
			// follow the address; libvirt refuses a second entry for it.
			name: "address reserved for another MAC",
			xml: `<network><ip address='10.200.1.1' netmask='255.255.255.0'><dhcp>` +
				`<host mac='52:54:00:3e:ee:43' name='old' ip='10.200.1.10'/></dhcp></ip></network>`,
			wantReserve: true,
			wantRemove:  []string{"52:54:00:3e:ee:43"},
		},
		{
			name: "other hosts reserved, no range",
			xml: `<network><ip address='10.200.1.1' netmask='255.255.255.0'><dhcp>` +
				`<host mac='52:54:00:00:00:01' name='w2' ip='10.200.1.11'/></dhcp></ip></network>`,
			wantReserve: true,
		},
		{
			// An IPv6 range gives no IPv4 address.
			name: "IPv6 range only",
			xml: `<network><ip address='10.200.1.1' netmask='255.255.255.0'/>` +
				`<ip family='ipv6' address='fd00::1' prefix='64'><dhcp><range start='fd00::10' end='fd00::ff'/></dhcp></ip></network>`,
			wantReserve: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reserve, remove, err := dhcpReservationPlan(tc.xml, mac, ip)
			if err != nil {
				t.Fatalf("dhcpReservationPlan: %v", err)
			}
			if reserve != tc.wantReserve || !slices.Equal(remove, tc.wantRemove) {
				t.Errorf("dhcpReservationPlan() = %v, %v, want %v, %v", reserve, remove, tc.wantReserve, tc.wantRemove)
			}
		})
	}
	if _, _, err := dhcpReservationPlan("not xml", mac, ip); err == nil {
		t.Error("expected an error for invalid XML")
	}
}

func TestDHCPReservedFor(t *testing.T) {
	const xml = `<network><ip address='10.200.1.1' netmask='255.255.255.0'><dhcp>` +
		`<host mac='52:54:00:aa:bb:cc' name='w1' ip='10.200.1.10'/></dhcp></ip></network>`
	if reserved, err := dhcpReservedFor(xml, "52:54:00:AA:BB:CC"); err != nil || !reserved {
		t.Errorf("dhcpReservedFor(reserved mac) = %v, %v", reserved, err)
	}
	if reserved, err := dhcpReservedFor(xml, "52:54:00:00:00:01"); err != nil || reserved {
		t.Errorf("dhcpReservedFor(other mac) = %v, %v", reserved, err)
	}
}
