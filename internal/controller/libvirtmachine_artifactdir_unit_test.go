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

func TestRemoveIgnitionDir(t *testing.T) {
	for _, tc := range []struct {
		name    string
		format  infrav1.BootstrapFormat
		rmdir   error
		wantDir string
	}{
		{name: "ignition machine", format: infrav1.BootstrapFormatIgnition, wantDir: "/run/caplv/ns-cl-m1"},
		// An older caplv-hostctl refuses the action; deletion goes on.
		{name: "helper without rmdir", format: infrav1.BootstrapFormatIgnition,
			rmdir: errors.New("caplv-hostctl: usage"), wantDir: "/run/caplv/ns-cl-m1"},
		// Cloud-init machines keep their bootstrap ISO in a storage pool.
		{name: "cloud-init machine", format: infrav1.BootstrapFormatCloudInit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var removed string
			c := &libvirt.MockClient{RemoveRemoteDirFn: func(_ context.Context, path string) error {
				removed = path
				return tc.rmdir
			}}
			lm := &infrav1.LibvirtMachine{Spec: infrav1.LibvirtMachineSpec{BootstrapFormat: tc.format}}

			removeIgnitionDir(context.Background(), c, lm, "/run/caplv/ns-cl-m1/ignition.json")

			if removed != tc.wantDir {
				t.Errorf("removed %q, want %q", removed, tc.wantDir)
			}
		})
	}
}
