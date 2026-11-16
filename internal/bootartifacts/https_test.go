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
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestHTTPSResolver_Resolve(t *testing.T) {
	kernel := []byte("kernel-bytes")
	initramfs := []byte("initramfs-bytes")
	kernelDigest := sha256Hex(kernel)
	initramfsDigest := sha256Hex(initramfs)

	mux := http.NewServeMux()
	mux.HandleFunc("/vmlinuz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(kernel)
	})
	mux.HandleFunc("/initramfs.img", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(initramfs)
	})
	mux.HandleFunc("/404", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	tls := httptest.NewTLSServer(mux)
	defer tls.Close()
	plain := httptest.NewServer(mux)
	defer plain.Close()

	cases := []struct {
		name        string
		src         infrav1.BootArtifactsSource
		wantErr     bool
		errContains string
	}{
		{
			name: "success without digests",
			src: infrav1.BootArtifactsSource{
				Type: infrav1.BootArtifactsSourceHTTPS,
				HTTPS: &infrav1.HTTPSBootArtifactsSource{
					KernelURL:    tls.URL + "/vmlinuz",
					InitramfsURL: tls.URL + "/initramfs.img",
				},
			},
		},
		{
			name: "success with matching digests",
			src: infrav1.BootArtifactsSource{
				Type: infrav1.BootArtifactsSourceHTTPS,
				HTTPS: &infrav1.HTTPSBootArtifactsSource{
					KernelURL:       tls.URL + "/vmlinuz",
					InitramfsURL:    tls.URL + "/initramfs.img",
					KernelSHA256:    kernelDigest,
					InitramfsSHA256: initramfsDigest,
				},
			},
		},
		{
			name: "kernel digest mismatch",
			src: infrav1.BootArtifactsSource{
				Type: infrav1.BootArtifactsSourceHTTPS,
				HTTPS: &infrav1.HTTPSBootArtifactsSource{
					KernelURL:    tls.URL + "/vmlinuz",
					InitramfsURL: tls.URL + "/initramfs.img",
					KernelSHA256: "deadbeef",
				},
			},
			wantErr:     true,
			errContains: "sha256 mismatch",
		},
		{
			name: "plain http rejected",
			src: infrav1.BootArtifactsSource{
				Type: infrav1.BootArtifactsSourceHTTPS,
				HTTPS: &infrav1.HTTPSBootArtifactsSource{
					KernelURL:    plain.URL + "/vmlinuz",
					InitramfsURL: tls.URL + "/initramfs.img",
				},
			},
			wantErr:     true,
			errContains: "only https://",
		},
		{
			name: "non-200 response",
			src: infrav1.BootArtifactsSource{
				Type: infrav1.BootArtifactsSourceHTTPS,
				HTTPS: &infrav1.HTTPSBootArtifactsSource{
					KernelURL:    tls.URL + "/404",
					InitramfsURL: tls.URL + "/initramfs.img",
				},
			},
			wantErr:     true,
			errContains: "unexpected status 404",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &HTTPSResolver{Client: tls.Client(), Dir: t.TempDir()}
			got, err := r.Resolve(context.Background(), tc.src, nil)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tc.errContains) {
					t.Fatalf("expected error to contain %q, got %v", tc.errContains, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.KernelSHA256 != kernelDigest {
				t.Errorf("kernel digest: got %s, want %s", got.KernelSHA256, kernelDigest)
			}
			if got.InitramfsSHA256 != initramfsDigest {
				t.Errorf("initramfs digest: got %s, want %s", got.InitramfsSHA256, initramfsDigest)
			}
			if string(artifactBytes(t, got.KernelPath)) != string(kernel) {
				t.Errorf("kernel bytes differ")
			}
			if string(artifactBytes(t, got.InitramfsPath)) != string(initramfs) {
				t.Errorf("initramfs bytes differ")
			}
		})
	}
}

func TestMultiResolver_DispatchesByType(t *testing.T) {
	m := NewMultiResolver(t.TempDir())

	_, err := m.Resolve(context.Background(), infrav1.BootArtifactsSource{Type: "Garbage"}, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected unsupported source type error, got %v", err)
	}

	_, err = m.Resolve(context.Background(), infrav1.BootArtifactsSource{Type: infrav1.BootArtifactsSourceHTTPS}, nil)
	if err == nil || !strings.Contains(err.Error(), "https is required") {
		t.Fatalf("expected required-field error for HTTPS, got %v", err)
	}

	_, err = m.Resolve(context.Background(), infrav1.BootArtifactsSource{Type: infrav1.BootArtifactsSourceOCI}, nil)
	if err == nil || !strings.Contains(err.Error(), "oci is required") {
		t.Fatalf("expected required-field error for OCI, got %v", err)
	}

	_, err = m.Resolve(context.Background(), infrav1.BootArtifactsSource{Type: infrav1.BootArtifactsSourceS3}, nil)
	if err == nil || !strings.Contains(err.Error(), "s3 is required") {
		t.Fatalf("expected required-field error for S3, got %v", err)
	}
}
