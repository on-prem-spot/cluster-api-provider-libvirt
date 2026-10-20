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

package iso

import (
	"strings"
	"testing"
)

func TestNetworkConfig(t *testing.T) {
	got, err := NetworkConfig(StaticNetwork{
		MACAddress:    "52:54:00:AB:CD:EF",
		Addresses:     []string{"192.168.150.10/24"},
		Gateway:       "192.168.150.1",
		Nameservers:   []string{"192.168.150.1"},
		SearchDomains: []string{"example.com"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := `ethernets:
  primary:
    addresses:
    - 192.168.150.10/24
    dhcp4: false
    dhcp6: false
    match:
      macaddress: 52:54:00:ab:cd:ef
    nameservers:
      addresses:
      - 192.168.150.1
      search:
      - example.com
    routes:
    - to: 0.0.0.0/0
      via: 192.168.150.1
version: 2
`
	if string(got) != want {
		t.Errorf("NetworkConfig() =\n%s\nwant\n%s", got, want)
	}
}

func TestNetworkConfigIPv6Gateway(t *testing.T) {
	got, err := NetworkConfig(StaticNetwork{
		MACAddress: "52:54:00:00:00:01",
		Addresses:  []string{"fd00::10/64"},
		Gateway:    "fd00::1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "- to: ::/0\n"; !strings.Contains(string(got), want) {
		t.Errorf("NetworkConfig() =\n%s\nwant a default route %q", got, want)
	}
}

func TestNetworkConfigRequiresMACAndAddress(t *testing.T) {
	if _, err := NetworkConfig(StaticNetwork{Addresses: []string{"10.0.0.2/24"}}); err == nil {
		t.Error("expected an error without a MAC address")
	}
	if _, err := NetworkConfig(StaticNetwork{MACAddress: "52:54:00:00:00:01"}); err == nil {
		t.Error("expected an error without an address")
	}
}
