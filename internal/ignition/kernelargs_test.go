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

package ignition

import "testing"

func TestKernelNetworkArgs(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  NetworkConfig
		want string
	}{
		{
			name: "address, gateway and DNS",
			cfg: NetworkConfig{Addresses: []string{"10.200.1.10/24"}, Gateway: "10.200.1.1",
				DNSServers: []string{"10.200.1.1", "1.1.1.1"}},
			want: "rd.neednet=1 ip=10.200.1.10::10.200.1.1:255.255.255.0:worker-1:enp1s0:none " +
				"nameserver=10.200.1.1 nameserver=1.1.1.1",
		},
		{
			// As in the keyfile, DNS defaults to the gateway.
			name: "DNS defaults to the gateway",
			cfg:  NetworkConfig{Addresses: []string{"192.168.1.50/22"}, Gateway: "192.168.0.1"},
			want: "rd.neednet=1 ip=192.168.1.50::192.168.0.1:255.255.252.0:worker-1:enp1s0:none nameserver=192.168.0.1",
		},
		{
			// The keyfile configures IPv4 only, and the initramfs needs one address.
			name: "first IPv4 address",
			cfg:  NetworkConfig{Addresses: []string{"2001:db8::10/64", "10.0.0.5/16", "10.0.0.6/16"}},
			want: "rd.neednet=1 ip=10.0.0.5:::255.255.0.0:worker-1:enp1s0:none",
		},
		{name: "no IPv4 address", cfg: NetworkConfig{Addresses: []string{"2001:db8::10/64"}}},
		{name: "no address"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := KernelNetworkArgs(tc.cfg, "worker-1"); got != tc.want {
				t.Errorf("KernelNetworkArgs() = %q, want %q", got, tc.want)
			}
		})
	}
}
