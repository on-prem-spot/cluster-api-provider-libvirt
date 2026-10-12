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
	"net/http"
	"net/http/httptest"
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

func TestDecompressIfGzip(t *testing.T) {
	raw := []byte("plain payload")
	if got, err := decompressIfGzip(raw); err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("plain bytes should pass through: got %q err %v", got, err)
	}

	wrapped := gzipBytes(t, raw)
	if got, err := decompressIfGzip(wrapped); err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("gzipped bytes should decompress: got %q err %v", got, err)
	}

	corrupt := append([]byte{0x1f, 0x8b}, []byte("not really gzip")...)
	if _, err := decompressIfGzip(corrupt); err == nil {
		t.Fatalf("expected error for corrupt gzip stream")
	}

	if got, err := decompressIfGzip(nil); err != nil || got != nil {
		t.Fatalf("nil input should pass through: got %v err %v", got, err)
	}
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

	r := &HTTPSResolver{Client: tls.Client()}
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
	if !bytes.Equal(got.KernelBytes, kernel) {
		t.Fatalf("kernel: expected decompressed payload, got %q", got.KernelBytes)
	}
	if !bytes.Equal(got.InitramfsBytes, initramfs) {
		t.Fatalf("initramfs: expected raw payload, got %q", got.InitramfsBytes)
	}
}
