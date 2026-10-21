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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

// fakeS3 serves path-style GET requests for two known keys, optionally
// requiring an SigV4 Authorization header.
type fakeS3 struct {
	server      *httptest.Server
	bucket      string
	objects     map[string][]byte
	requireAuth bool
}

func newFakeS3(t *testing.T, bucket string, objects map[string][]byte, requireAuth bool) *fakeS3 {
	t.Helper()
	s := &fakeS3{bucket: bucket, objects: objects, requireAuth: requireAuth}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		if s.requireAuth && !strings.HasPrefix(req.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if req.URL.Query().Has("location") {
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<?xml version="1.0"?><LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">us-east-1</LocationConstraint>`))
			return
		}
		prefix := "/" + s.bucket + "/"
		key := strings.TrimPrefix(req.URL.Path, prefix)
		if !strings.HasPrefix(req.URL.Path, prefix) || key == "" {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`<?xml version="1.0"?><Error><Code>NoSuchBucket</Code><Message>The specified bucket does not exist.</Message></Error>`))
			return
		}
		blob, ok := s.objects[key]
		if !ok {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`<?xml version="1.0"?><Error><Code>NoSuchKey</Code><Message>The specified key does not exist.</Message></Error>`))
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		_, _ = w.Write(blob)
	})
	s.server = httptest.NewServer(mux)
	t.Cleanup(s.server.Close)
	return s
}

func (s *fakeS3) endpoint() string {
	return strings.TrimPrefix(s.server.URL, "http://")
}

func TestS3Resolver_Resolve(t *testing.T) {
	kernel := []byte("s3-kernel")
	initramfs := []byte("s3-initramfs")
	kSum := sha256Hex(kernel)
	iSum := sha256Hex(initramfs)

	fake := newFakeS3(t, "boot", map[string][]byte{
		"vmlinuz":       kernel,
		"initramfs.img": initramfs,
	}, false)

	cases := []struct {
		name        string
		src         infrav1.S3BootArtifactsSource
		creds       *Credentials
		wantErr     bool
		errContains string
	}{
		{
			name: "anonymous read",
			src: infrav1.S3BootArtifactsSource{
				Endpoint:     fake.endpoint(),
				Bucket:       "boot",
				KernelKey:    "vmlinuz",
				InitramfsKey: "initramfs.img",
				UsePathStyle: true,
				Insecure:     true,
			},
		},
		{
			name: "matching digests",
			src: infrav1.S3BootArtifactsSource{
				Endpoint:        fake.endpoint(),
				Bucket:          "boot",
				KernelKey:       "vmlinuz",
				InitramfsKey:    "initramfs.img",
				UsePathStyle:    true,
				Insecure:        true,
				KernelSHA256:    kSum,
				InitramfsSHA256: iSum,
			},
		},
		{
			name: "digest mismatch",
			src: infrav1.S3BootArtifactsSource{
				Endpoint:     fake.endpoint(),
				Bucket:       "boot",
				KernelKey:    "vmlinuz",
				InitramfsKey: "initramfs.img",
				UsePathStyle: true,
				Insecure:     true,
				KernelSHA256: "deadbeef",
			},
			wantErr:     true,
			errContains: "sha256 mismatch",
		},
		{
			name: "missing key",
			src: infrav1.S3BootArtifactsSource{
				Endpoint:     fake.endpoint(),
				Bucket:       "boot",
				KernelKey:    "does-not-exist",
				InitramfsKey: "initramfs.img",
				UsePathStyle: true,
				Insecure:     true,
			},
			wantErr:     true,
			errContains: "kernel",
		},
	}

	r := NewS3Resolver(t.TempDir())
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := r.Resolve(context.Background(), infrav1.BootArtifactsSource{
				Type: infrav1.BootArtifactsSourceS3,
				S3:   &tc.src,
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
		})
	}
}

func TestS3Resolver_AuthRequired(t *testing.T) {
	kernel := []byte("k")
	initramfs := []byte("i")
	fake := newFakeS3(t, "boot", map[string][]byte{
		"vmlinuz":       kernel,
		"initramfs.img": initramfs,
	}, true)

	r := NewS3Resolver(t.TempDir())
	src := infrav1.BootArtifactsSource{
		Type: infrav1.BootArtifactsSourceS3,
		S3: &infrav1.S3BootArtifactsSource{
			Endpoint:     fake.endpoint(),
			Bucket:       "boot",
			KernelKey:    "vmlinuz",
			InitramfsKey: "initramfs.img",
			UsePathStyle: true,
			Insecure:     true,
		},
	}

	if _, err := r.Resolve(context.Background(), src, nil); err == nil {
		t.Fatalf("expected anonymous read to fail when auth is required")
	}

	got, err := r.Resolve(context.Background(), src, &Credentials{
		AccessKeyID:     "AKIAEXAMPLE",
		SecretAccessKey: "secret",
	})
	if err != nil {
		t.Fatalf("authenticated read failed: %v", err)
	}
	if string(artifactBytes(t, got.KernelPath)) != "k" || string(artifactBytes(t, got.InitramfsPath)) != "i" {
		t.Errorf("unexpected payload")
	}
}
