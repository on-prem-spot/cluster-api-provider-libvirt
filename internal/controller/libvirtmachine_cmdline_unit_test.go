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
	"testing"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

func TestDirectBootCmdline(t *testing.T) {
	const kernelArgs = "ignition.platform.id=metal ignition.config.url=oem:/dev/disk/by-id/virtio-ignition"
	static := infrav1.NetworkSpec{
		Addresses: []string{"10.200.1.10/24"},
		Gateway:   "10.200.1.1",
		DNS:       &infrav1.DNSSpec{Nameservers: []string{"10.200.1.1"}},
	}
	for _, tc := range []struct {
		name       string
		kernelArgs string
		format     infrav1.BootstrapFormat
		network    infrav1.NetworkSpec
		want       string
	}{
		{
			name: "ignition with a static address", kernelArgs: kernelArgs,
			format: infrav1.BootstrapFormatIgnition, network: static,
			want: kernelArgs + " rd.neednet=1 ip=10.200.1.10::10.200.1.1:255.255.255.0:worker-1:enp1s0:none " +
				"nameserver=10.200.1.1",
		},
		{
			name:       "kernelArgs that set ip= are left alone",
			kernelArgs: kernelArgs + " ip=dhcp", format: infrav1.BootstrapFormatIgnition, network: static,
			want: kernelArgs + " ip=dhcp",
		},
		{
			name: "no static address", kernelArgs: kernelArgs,
			format: infrav1.BootstrapFormatIgnition, network: infrav1.NetworkSpec{},
			want: kernelArgs,
		},
		{
			name: "cloud-init", kernelArgs: kernelArgs,
			format: infrav1.BootstrapFormatCloudInit, network: static,
			want: kernelArgs,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := directBootCmdline(tc.kernelArgs, tc.format, tc.network, "worker-1"); got != tc.want {
				t.Errorf("directBootCmdline() =\n  %q\nwant\n  %q", got, tc.want)
			}
		})
	}
}
