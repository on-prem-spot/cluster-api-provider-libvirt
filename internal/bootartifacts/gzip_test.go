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
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

func gzipBytes(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(raw); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func TestStoreArtifact(t *testing.T) {
	raw := []byte("plain payload")
	for _, tc := range []struct {
		name    string
		input   []byte
		digest  string
		want    []byte
		wantErr bool
	}{
		{name: "plain bytes pass through", input: raw, want: raw},
		{name: "gzipped bytes are decompressed", input: gzipBytes(t, raw), want: raw},
		{name: "the digest is of the payload", input: gzipBytes(t, raw), digest: sha256Hex(raw), want: raw},
		{name: "a wrong digest is refused", input: raw, digest: sha256Hex([]byte("other")), wantErr: true},
		{name: "corrupt gzip is refused", input: append([]byte{0x1f, 0x8b}, []byte("not really gzip")...),
			wantErr: true},
		{name: "an empty payload is stored", input: nil, want: []byte{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path, digest, err := storeArtifact(dir, bytes.NewReader(tc.input), tc.digest, "test artifact")
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				if entries, _ := os.ReadDir(dir); len(entries) != 0 {
					t.Errorf("expected nothing left in the cache, found %d entries", len(entries))
				}
				return
			}
			if err != nil {
				t.Fatalf("storeArtifact: %v", err)
			}
			if got := artifactBytes(t, path); !bytes.Equal(got, tc.want) {
				t.Errorf("stored %q, want %q", got, tc.want)
			}
			if digest != sha256Hex(tc.want) || filepath.Base(path) != digest {
				t.Errorf("path %s, digest %s; want both named after %s", path, digest, sha256Hex(tc.want))
			}
		})
	}
}

func TestCappedReader(t *testing.T) {
	if got, err := io.ReadAll(newCappedReader(bytes.NewReader([]byte("12345")), 5, "x")); err != nil ||
		string(got) != "12345" {
		t.Errorf("a payload at the limit: got %q, %v", got, err)
	}
	if _, err := io.ReadAll(newCappedReader(bytes.NewReader([]byte("123456")), 5, "x")); err == nil {
		t.Error("expected a payload over the limit to fail rather than be truncated")
	}
}

// artifactBytes reads a stored artifact.
func artifactBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // a path the test's resolver returned
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	return data
}

func TestHTTPSResolver_TransparentGzip(t *testing.T) {
	kernel := []byte("kernel-payload")
	initramfs := []byte("initramfs-payload")
	kSum := sha256Hex(kernel)
	iSum := sha256Hex(initramfs)

	mux := http.NewServeMux()
	mux.HandleFunc("/vmlinuz.gz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(gzipBytes(t, kernel))
	})
	mux.HandleFunc("/initramfs.img", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(initramfs)
	})
	tls := httptest.NewTLSServer(mux)
	defer tls.Close()

	r := &HTTPSResolver{Client: tls.Client(), Dir: t.TempDir()}
	got, err := r.Resolve(context.Background(), infrav1.BootArtifactsSource{
		Type: infrav1.BootArtifactsSourceHTTPS,
		HTTPS: &infrav1.HTTPSBootArtifactsSource{
			KernelURL:       tls.URL + "/vmlinuz.gz",
			InitramfsURL:    tls.URL + "/initramfs.img",
			KernelSHA256:    kSum, // digest of the *decompressed* kernel
			InitramfsSHA256: iSum,
		},
	}, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got := artifactBytes(t, got.KernelPath); !bytes.Equal(got, kernel) {
		t.Fatalf("kernel: expected decompressed payload, got %q", got)
	}
	if got := artifactBytes(t, got.InitramfsPath); !bytes.Equal(got, initramfs) {
		t.Fatalf("initramfs: expected raw payload, got %q", got)
	}
}
