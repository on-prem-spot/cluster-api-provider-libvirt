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

package imagecache

import (
	"context"
	"strings"
	"testing"

	infrav1 "github.com/atgreen/caplv/api/v1alpha1"
)

// The base image becomes the root disk of every worker VM, so a transport that
// leaves the peer unauthenticated lets anyone on the network path choose what
// those nodes boot. Refused unless the operator set
// --allow-insecure-image-transport.

func TestCheckTransportPolicy_RejectsInsecureSources(t *testing.T) {
	cases := map[string]infrav1.BaseImageSource{
		"https.url is plain http": {
			Type:  infrav1.BootArtifactsSourceHTTPS,
			HTTPS: &infrav1.HTTPSBaseImageSource{URL: "http://mirror.example.com/i.qcow2"},
		},
		"https.url is plain http, upper case": {
			Type:  infrav1.BootArtifactsSourceHTTPS,
			HTTPS: &infrav1.HTTPSBaseImageSource{URL: "HTTP://mirror.example.com/i.qcow2"},
		},
		"https.insecureSkipTLSVerify": {
			Type: infrav1.BootArtifactsSourceHTTPS,
			HTTPS: &infrav1.HTTPSBaseImageSource{
				URL:                   "https://mirror.example.com/i.qcow2",
				InsecureSkipTLSVerify: true,
			},
		},
		"oci.plainHTTP": {
			Type: infrav1.BootArtifactsSourceOCI,
			OCI:  &infrav1.OCIBaseImageSource{Reference: "r/x:1", PlainHTTP: true},
		},
		"oci.insecureSkipTLSVerify": {
			Type: infrav1.BootArtifactsSourceOCI,
			OCI:  &infrav1.OCIBaseImageSource{Reference: "r/x:1", InsecureSkipTLSVerify: true},
		},
		"s3.insecure": {
			Type: infrav1.BootArtifactsSourceS3,
			S3:   &infrav1.S3BaseImageSource{Endpoint: "s3.example.com", Insecure: true},
		},
		"s3.insecureSkipTLSVerify": {
			Type: infrav1.BootArtifactsSourceS3,
			S3:   &infrav1.S3BaseImageSource{Endpoint: "s3.example.com", InsecureSkipTLSVerify: true},
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
	cases := map[string]infrav1.BaseImageSource{
		"https": {
			Type:  infrav1.BootArtifactsSourceHTTPS,
			HTTPS: &infrav1.HTTPSBaseImageSource{URL: "https://mirror.example.com/i.qcow2"},
		},
		"oci": {
			Type: infrav1.BootArtifactsSourceOCI,
			OCI:  &infrav1.OCIBaseImageSource{Reference: "quay.io/x/y:1"},
		},
		"s3": {
			Type: infrav1.BootArtifactsSourceS3,
			S3:   &infrav1.S3BaseImageSource{Endpoint: "s3.example.com"},
		},
	}
	for name, src := range cases {
		if err := checkTransportPolicy(src, false); err != nil {
			t.Errorf("%s: expected acceptance, got: %v", name, err)
		}
	}
}

// The policy has to be enforced on the dispatch path, not only in the helper.
// Only the refusal is asserted here: the opt-in path is covered by
// checkTransportPolicy above, and exercising it through Open would require a
// network round-trip.
func TestMultiResolver_EnforcesTransportPolicy(t *testing.T) {
	m := NewMultiResolver()
	src := infrav1.BaseImageSource{
		Type:  infrav1.BootArtifactsSourceHTTPS,
		HTTPS: &infrav1.HTTPSBaseImageSource{URL: "http://mirror.example.com/i.qcow2"},
	}
	_, err := m.Open(context.Background(), src, nil)
	if err == nil {
		t.Fatal("expected MultiResolver.Open to refuse a plain-http source")
	}
	if !strings.Contains(err.Error(), "plain http") {
		t.Errorf("expected the policy gate to reject it, got: %v", err)
	}
}
