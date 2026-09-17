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

package bootartifacts

import (
	"context"
	"testing"

	infrav1 "github.com/atgreen/caplv/api/v1alpha1"
)

// --- transport policy ---
//
// These bytes become the kernel and initramfs every VM boots, so a transport
// that leaves the peer unauthenticated is refused unless the operator opted in
// with --allow-insecure-image-transport.

func TestCheckTransportPolicy_RejectsInsecureSources(t *testing.T) {
	cases := map[string]infrav1.BootArtifactsSource{
		"oci.plainHTTP": {
			Type: infrav1.BootArtifactsSourceOCI,
			OCI:  &infrav1.OCIBootArtifactsSource{Reference: "r/x:1", PlainHTTP: true},
		},
		"oci.insecureSkipTLSVerify": {
			Type: infrav1.BootArtifactsSourceOCI,
			OCI:  &infrav1.OCIBootArtifactsSource{Reference: "r/x:1", InsecureSkipTLSVerify: true},
		},
		"s3.insecure": {
			Type: infrav1.BootArtifactsSourceS3,
			S3:   &infrav1.S3BootArtifactsSource{Endpoint: "s3.example.com", Insecure: true},
		},
		"s3.insecureSkipTLSVerify": {
			Type: infrav1.BootArtifactsSourceS3,
			S3:   &infrav1.S3BootArtifactsSource{Endpoint: "s3.example.com", InsecureSkipTLSVerify: true},
		},
	}
	for name, src := range cases {
		if err := checkTransportPolicy(src, false); err == nil {
			t.Errorf("%s: expected rejection by default", name)
		}
		if err := checkTransportPolicy(src, true); err != nil {
			t.Errorf("%s: expected acceptance when opted in, got: %v", name, err)
		}
	}
}

func TestCheckTransportPolicy_AllowsSecureSources(t *testing.T) {
	cases := map[string]infrav1.BootArtifactsSource{
		"https": {
			Type:  infrav1.BootArtifactsSourceHTTPS,
			HTTPS: &infrav1.HTTPSBootArtifactsSource{KernelURL: "https://m/vmlinuz"},
		},
		"oci": {
			Type: infrav1.BootArtifactsSourceOCI,
			OCI:  &infrav1.OCIBootArtifactsSource{Reference: "quay.io/x/y:1"},
		},
		"s3": {
			Type: infrav1.BootArtifactsSourceS3,
			S3:   &infrav1.S3BootArtifactsSource{Endpoint: "s3.example.com"},
		},
	}
	for name, src := range cases {
		if err := checkTransportPolicy(src, false); err != nil {
			t.Errorf("%s: expected acceptance, got: %v", name, err)
		}
	}
}

// The policy has to be enforced on the dispatch path, not only in the helper.
func TestMultiResolver_EnforcesTransportPolicy(t *testing.T) {
	m := NewMultiResolver()
	src := infrav1.BootArtifactsSource{
		Type: infrav1.BootArtifactsSourceOCI,
		OCI:  &infrav1.OCIBootArtifactsSource{Reference: "r/x:1", PlainHTTP: true},
	}
	if _, err := m.Resolve(context.Background(), src, nil); err == nil {
		t.Fatal("expected MultiResolver.Resolve to refuse an insecure source")
	}
}
