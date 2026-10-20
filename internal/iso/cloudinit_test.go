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
	"bytes"
	"testing"
)

func TestBuildCloudInitISO(t *testing.T) {
	builder := NewDiskfsBuilder()
	userData := []byte("#cloud-config\npackages:\n  - vim\n")

	networkConfig := []byte("version: 2\n")

	data, err := builder.BuildCloudInitISO(userData, networkConfig, "test-instance-id", "test-host",
		"libvirt:///host-1/test-instance-id")
	if err != nil {
		t.Fatalf("BuildCloudInitISO returned unexpected error: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("BuildCloudInitISO returned empty data")
	}
	// cloud-init's NoCloud datasource finds the seed by its volume label and
	// reads these exact file names.
	assertVolumeID(t, data, "cidata")
	assertFileNames(t, data, "user-data", "meta-data", "network-config")
	if !bytes.Contains(data, []byte("provider_id: libvirt:///host-1/test-instance-id\n")) {
		t.Error("meta-data does not carry the provider ID")
	}
}
