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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

// fakeRegistry serves just the OCI distribution-spec endpoints that the
// resolver needs: GET /v2/{name}/manifests/{ref} and GET /v2/{name}/blobs/{digest}.
type fakeRegistry struct {
	t        *testing.T
	server   *httptest.Server
	manifest []byte
	blobs    map[digest.Digest][]byte
	wantAuth string // expected Authorization header; empty disables the check.
}

func newFakeRegistry(t *testing.T, kernel, initramfs []byte, opts ...func(*fakeRegistry)) *fakeRegistry {
	t.Helper()
	r := &fakeRegistry{t: t, blobs: map[digest.Digest][]byte{}}

	kDesc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(kernel),
		Size:      int64(len(kernel)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: defaultKernelLayerTitle,
		},
	}
	iDesc := ocispec.Descriptor{
		MediaType: "application/octet-stream",
		Digest:    digest.FromBytes(initramfs),
		Size:      int64(len(initramfs)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: defaultInitramfsLayerTitle,
		},
	}
	cfg := []byte("{}")
	cfgDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageConfig,
		Digest:    digest.FromBytes(cfg),
		Size:      int64(len(cfg)),
	}
	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    cfgDesc,
		Layers:    []ocispec.Descriptor{kDesc, iDesc},
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	r.manifest = body
	r.blobs[cfgDesc.Digest] = cfg
	r.blobs[kDesc.Digest] = kernel
	r.blobs[iDesc.Digest] = initramfs

	for _, o := range opts {
		o(r)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, req *http.Request) {
		if r.wantAuth != "" && req.Header.Get("Authorization") != r.wantAuth {
			w.Header().Set("Www-Authenticate", `Basic realm="test"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		path := req.URL.Path
		switch {
		case path == "/v2/":
			w.WriteHeader(http.StatusOK)
		case strings.Contains(path, "/manifests/"):
			w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", string(digest.FromBytes(r.manifest)))
			_, _ = w.Write(r.manifest)
		case strings.Contains(path, "/blobs/"):
			parts := strings.Split(path, "/blobs/")
			d := digest.Digest(parts[1])
			blob, ok := r.blobs[d]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(blob)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	r.server = httptest.NewServer(mux)
	t.Cleanup(r.server.Close)
	return r
}

func withRequiredAuth(header string) func(*fakeRegistry) {
	return func(r *fakeRegistry) { r.wantAuth = header }
}

func (r *fakeRegistry) host() string {
	return strings.TrimPrefix(r.server.URL, "http://")
}

func TestOCIResolver_Resolve(t *testing.T) {
	kernel := []byte("oci-kernel")
	initramfs := []byte("oci-initramfs")
	kSum := sha256Hex(kernel)
	iSum := sha256Hex(initramfs)

	reg := newFakeRegistry(t, kernel, initramfs)
	ref := fmt.Sprintf("%s/caplv/boot:v1", reg.host())

	cases := []struct {
		name        string
		src         infrav1.OCIBootArtifactsSource
		creds       *Credentials
		wantErr     bool
		errContains string
	}{
		{
			name: "success defaults",
			src:  infrav1.OCIBootArtifactsSource{Reference: ref, PlainHTTP: true},
		},
		{
			name: "success with digests",
			src: infrav1.OCIBootArtifactsSource{
				Reference:       ref,
				PlainHTTP:       true,
				KernelSHA256:    kSum,
				InitramfsSHA256: iSum,
			},
		},
		{
			name: "kernel digest mismatch",
			src: infrav1.OCIBootArtifactsSource{
				Reference:    ref,
				PlainHTTP:    true,
				KernelSHA256: "deadbeef",
			},
			wantErr:     true,
			errContains: "sha256 mismatch",
		},
		{
			name: "missing layer",
			src: infrav1.OCIBootArtifactsSource{
				Reference:        ref,
				PlainHTTP:        true,
				KernelLayerTitle: "does-not-exist",
			},
			wantErr:     true,
			errContains: "no layer titled",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewOCIResolver(t.TempDir())
			got, err := r.Resolve(context.Background(), infrav1.BootArtifactsSource{
				Type: infrav1.BootArtifactsSourceOCI,
				OCI:  &tc.src,
			}, tc.creds)
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
			if got.KernelSHA256 != kSum {
				t.Errorf("kernel digest: got %s, want %s", got.KernelSHA256, kSum)
			}
			if got.InitramfsSHA256 != iSum {
				t.Errorf("initramfs digest: got %s, want %s", got.InitramfsSHA256, iSum)
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

func TestOCIResolver_BasicAuth(t *testing.T) {
	kernel := []byte("k")
	initramfs := []byte("i")
	expectedAuth := "Basic " + basicAuthEncode("alice", "s3cr3t")

	reg := newFakeRegistry(t, kernel, initramfs, withRequiredAuth(expectedAuth))
	ref := fmt.Sprintf("%s/caplv/boot:v1", reg.host())

	r := NewOCIResolver(t.TempDir())
	src := infrav1.BootArtifactsSource{
		Type: infrav1.BootArtifactsSourceOCI,
		OCI:  &infrav1.OCIBootArtifactsSource{Reference: ref, PlainHTTP: true},
	}

	if _, err := r.Resolve(context.Background(), src, nil); err == nil {
		t.Fatalf("expected unauthenticated pull to fail")
	}

	got, err := r.Resolve(context.Background(), src, &Credentials{Username: "alice", Password: "s3cr3t"})
	if err != nil {
		t.Fatalf("authenticated pull failed: %v", err)
	}
	if string(artifactBytes(t, got.KernelPath)) != "k" || string(artifactBytes(t, got.InitramfsPath)) != "i" {
		t.Errorf("unexpected payload")
	}
}

func basicAuthEncode(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}
