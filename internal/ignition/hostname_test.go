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

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

const (
	testIgnitionV3Input = `{"ignition":{"version":"3.2.0"},"storage":{"files":[]}}`
	testEtcHostnamePath = "/etc/hostname"
)

func TestInjectHostnameV3(t *testing.T) {
	input := testIgnitionV3Input
	result, err := InjectHostname([]byte(input), "my-worker")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var config map[string]any
	if err := json.Unmarshal(result, &config); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	files := config["storage"].(map[string]any)["files"].([]any)
	found := false
	for _, f := range files {
		fm := f.(map[string]any)
		if fm["path"] == testEtcHostnamePath {
			contents := fm["contents"].(map[string]any)
			source := contents["source"].(string)
			if !strings.Contains(source, "my-worker") {
				t.Errorf("hostname source = %q, want to contain 'my-worker'", source)
			}
			found = true
		}
	}
	if !found {
		t.Error("expected /etc/hostname file in ignition output")
	}
}

func TestInjectHostnameV2(t *testing.T) {
	input := `{"ignition":{"version":"2.2.0"},"storage":{"files":[]}}`
	result, err := InjectHostname([]byte(input), "my-worker")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var config map[string]any
	if err := json.Unmarshal(result, &config); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	files := config["storage"].(map[string]any)["files"].([]any)
	found := false
	for _, f := range files {
		fm := f.(map[string]any)
		if fm["path"] == testEtcHostnamePath {
			found = true
		}
	}
	if !found {
		t.Error("expected /etc/hostname file in ignition output")
	}
}

func TestInjectHostnameReplacesExisting(t *testing.T) {
	input := `{"ignition":{"version":"3.2.0"},"storage":{"files":[{"path":"/etc/hostname","contents":{"source":"data:,old-name"}}]}}`
	result, err := InjectHostname([]byte(input), "new-name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var config map[string]any
	if err := json.Unmarshal(result, &config); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	files := config["storage"].(map[string]any)["files"].([]any)
	count := 0
	for _, f := range files {
		fm := f.(map[string]any)
		if fm["path"] == testEtcHostnamePath {
			count++
			contents := fm["contents"].(map[string]any)
			if !strings.Contains(contents["source"].(string), "new-name") {
				t.Error("hostname not updated to new-name")
			}
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 /etc/hostname entry, got %d", count)
	}
}

func TestInjectMachineMetadata(t *testing.T) {
	input := testIgnitionV3Input
	result, err := InjectMachineMetadata([]byte(input), "my-worker", "libvirt:///laptop/my-worker")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var config map[string]any
	if err := json.Unmarshal(result, &config); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	files := config["storage"].(map[string]any)["files"].([]any)

	paths := make(map[string]bool)
	for _, f := range files {
		fm := f.(map[string]any)
		paths[fm["path"].(string)] = true
	}

	if !paths[testEtcHostnamePath] {
		t.Error("expected /etc/hostname file")
	}
	if !paths["/etc/systemd/system/kubelet.service.d/90-provider-id.conf"] {
		t.Error("expected kubelet provider-id drop-in")
	}
}

func TestInjectMachineMetadataProviderIDContent(t *testing.T) {
	input := testIgnitionV3Input
	result, err := InjectMachineMetadata([]byte(input), "", "libvirt:///host/domain")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var config map[string]any
	if err := json.Unmarshal(result, &config); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	files := config["storage"].(map[string]any)["files"].([]any)
	for _, f := range files {
		fm := f.(map[string]any)
		if fm["path"] == "/etc/systemd/system/kubelet.service.d/90-provider-id.conf" {
			contents := fm["contents"].(map[string]any)
			source := contents["source"].(string)
			// OpenShift's kubelet.service passes --provider-id=${KUBELET_PROVIDERID}.
			want := "[Service]\nEnvironment=\"KUBELET_PROVIDERID=libvirt:///host/domain\"\n"
			got, err := url.PathUnescape(strings.TrimPrefix(source, "data:,"))
			if err != nil {
				t.Fatalf("unescaping drop-in source %q: %v", source, err)
			}
			if got != want {
				t.Errorf("drop-in = %q, want %q", got, want)
			}
			return
		}
	}
	t.Error("provider-id drop-in not found")
}
