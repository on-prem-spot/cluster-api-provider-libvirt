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

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
	"github.com/on-prem-spot/cluster-api-provider-libvirt/internal/libvirt"
)

func TestReserveDHCPAddress(t *testing.T) {
	for _, tc := range []struct {
		name    string
		network infrav1.NetworkSpec
		fail    bool
		want    string // "network mac ip name", or "" for no reservation
	}{
		{
			name: "libvirt network with a static address",
			network: infrav1.NetworkSpec{Type: infrav1.NetworkTypeNetwork, Name: "caplv",
				Addresses: []string{"10.200.1.10/24"}},
			want: "caplv 52:54:00:aa:bb:cc 10.200.1.10 worker-1",
		},
		{
			name: "the first IPv4 address",
			network: infrav1.NetworkSpec{Type: infrav1.NetworkTypeNetwork, Name: "caplv",
				Addresses: []string{"2001:db8::10/64", "10.200.1.10/24"}},
			want: "caplv 52:54:00:aa:bb:cc 10.200.1.10 worker-1",
		},
		{
			// A failure is reported but does not stop provisioning.
			name: "reservation fails",
			network: infrav1.NetworkSpec{Type: infrav1.NetworkTypeNetwork, Name: "caplv",
				Addresses: []string{"10.200.1.10/24"}},
			fail: true,
			want: "caplv 52:54:00:aa:bb:cc 10.200.1.10 worker-1",
		},
		{
			// A bridge's DHCP, if any, is not CAPLV's to configure.
			name: "bridge",
			network: infrav1.NetworkSpec{Type: infrav1.NetworkTypeBridge, Name: "br0",
				Addresses: []string{"10.200.1.10/24"}},
		},
		{
			name:    "no IPv4 address",
			network: infrav1.NetworkSpec{Type: infrav1.NetworkTypeNetwork, Name: "caplv"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			c := &libvirt.MockClient{ReserveDHCPAddressFn: func(_ context.Context, network, mac, ip, name string) error {
				got = network + " " + mac + " " + ip + " " + name
				if tc.fail {
					return errors.New("net-update failed")
				}
				return nil
			}}

			reserveDHCPAddress(context.Background(), nil, c, &infrav1.LibvirtMachine{}, tc.network,
				"52:54:00:aa:bb:cc", "worker-1")

			if got != tc.want {
				t.Errorf("reserved %q, want %q", got, tc.want)
			}
		})
	}
}
