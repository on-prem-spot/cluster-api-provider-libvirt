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
	"bytes"
	"compress/gzip"
	"context"
	"strings"
	"testing"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
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
	m := NewMultiResolver(t.TempDir())
	src := infrav1.BootArtifactsSource{
		Type: infrav1.BootArtifactsSourceOCI,
		OCI:  &infrav1.OCIBootArtifactsSource{Reference: "r/x:1", PlainHTTP: true},
	}
	if _, err := m.Resolve(context.Background(), src, nil); err == nil {
		t.Fatal("expected MultiResolver.Resolve to refuse an insecure source")
	}
}

// --- read limits ---

func TestReadLimited_RejectsOversizedInput(t *testing.T) {
	r := bytes.NewReader(make([]byte, 64))
	if _, err := readLimited(r, 32, "artifact"); err == nil {
		t.Fatal("expected an oversized read to be rejected, not truncated")
	}
}

func TestReadLimited_AcceptsInputAtTheLimit(t *testing.T) {
	r := bytes.NewReader(make([]byte, 32))
	got, err := readLimited(r, 32, "artifact")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 32 {
		t.Errorf("expected 32 bytes, got %d", len(got))
	}
}

// A decompression bomb is small on the wire and huge once expanded, and the
// digest can only be checked after the bytes are in hand — so a pinned sha256
// is no defence and the cap has to apply to the *decompressed* stream. Tested
// through readLimited with a small limit rather than the production
// MaxArtifactBytes, so the assertion does not cost half a gigabyte of heap.
func TestReadLimited_RejectsDecompressionBomb(t *testing.T) {
	const limit = 1 << 20

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(make([]byte, limit*8)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	if buf.Len() >= limit {
		t.Fatalf("expected the compressed form to be far under the limit, got %d bytes", buf.Len())
	}

	zr, err := gzip.NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	if _, err := readLimited(zr, limit, "gzip-decompressed artifact"); err == nil {
		t.Fatal("expected the decompressed size cap to reject the bomb")
	} else if !strings.Contains(err.Error(), "limit") {
		t.Errorf("expected a limit error, got: %v", err)
	}
}

// Guard the production cap itself, since the test above uses a smaller one.
func TestMaxArtifactBytesIsBounded(t *testing.T) {
	if MaxArtifactBytes <= 0 || MaxArtifactBytes > 1<<30 {
		t.Errorf("MaxArtifactBytes = %d, want a positive cap no larger than 1 GiB", MaxArtifactBytes)
	}
}
