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
	"fmt"
	"strings"

	"sigs.k8s.io/yaml"
)

// StaticNetwork is the static addressing of a guest's NIC.
type StaticNetwork struct {
	// MACAddress identifies the NIC.
	MACAddress string
	// Addresses are in CIDR notation.
	Addresses     []string
	Gateway       string
	Nameservers   []string
	SearchDomains []string
}

// NetworkConfig renders n as a cloud-init network configuration, version 2,
// for the NoCloud network-config file. cloud-init translates it for the
// guest's own network stack (netplan, NetworkManager, ifupdown and so on),
// so the same file configures Ubuntu, Debian and Enterprise Linux guests.
// The NIC is matched by MAC address, since interface names differ between
// distributions (enp1s0, eth0).
func NetworkConfig(n StaticNetwork) ([]byte, error) {
	if n.MACAddress == "" {
		return nil, fmt.Errorf("a MAC address is required to match the NIC")
	}
	if len(n.Addresses) == 0 {
		return nil, fmt.Errorf("at least one address is required")
	}

	ethernet := map[string]any{
		"match":     map[string]any{"macaddress": strings.ToLower(n.MACAddress)},
		"addresses": n.Addresses,
		"dhcp4":     false,
		"dhcp6":     false,
	}
	if n.Gateway != "" {
		// "default" is newer than some cloud-init releases; spell it out.
		to := "0.0.0.0/0"
		if strings.Contains(n.Gateway, ":") {
			to = "::/0"
		}
		ethernet["routes"] = []map[string]any{{"to": to, "via": n.Gateway}}
	}
	if len(n.Nameservers) > 0 || len(n.SearchDomains) > 0 {
		nameservers := map[string]any{}
		if len(n.Nameservers) > 0 {
			nameservers["addresses"] = n.Nameservers
		}
		if len(n.SearchDomains) > 0 {
			nameservers["search"] = n.SearchDomains
		}
		ethernet["nameservers"] = nameservers
	}

	return yaml.Marshal(map[string]any{
		"version":   2,
		"ethernets": map[string]any{"primary": ethernet},
	})
}
