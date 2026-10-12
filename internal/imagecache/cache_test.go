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
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

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

// stubResolver returns a fixed payload, counting the number of times Open
// was called.
type stubResolver struct {
	payload []byte
	openErr error
	calls   atomic.Int64
}

func (s *stubResolver) Open(_ context.Context, _ infrav1.BaseImageSource, _ *Credentials) (io.ReadCloser, error) {
	s.calls.Add(1)
	if s.openErr != nil {
		return nil, s.openErr
	}
	return io.NopCloser(bytes.NewReader(s.payload)), nil
}

func TestCache_Get_HitMissAndDigest(t *testing.T) {
	dir := t.TempDir()
	payload := []byte("qcow2-bytes")
	want := sha256Hex(payload)

	res := &stubResolver{payload: payload}
	c, err := New(dir, res)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	src := infrav1.BaseImageSource{
		Type: infrav1.BootArtifactsSourceHTTPS,
		HTTPS: &infrav1.HTTPSBaseImageSource{
			URL: "https://example.test/x.qcow2",
		},
	}

	first, err := c.Get(context.Background(), src, nil, "")
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	if first.SHA256 != want {
		t.Errorf("digest: got %s, want %s", first.SHA256, want)
	}
	if _, err := os.Stat(first.Path); err != nil {
		t.Fatalf("cached file missing: %v", err)
	}

	// Expected digest supplied + file already cached → no network.
	res.calls.Store(0)
	second, err := c.Get(context.Background(), src, nil, want)
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if second.Path != first.Path {
		t.Errorf("expected same cache path, got %s vs %s", second.Path, first.Path)
	}
	if got := res.calls.Load(); got != 0 {
		t.Errorf("expected no Open call when cached, got %d", got)
	}
}

func TestCache_Get_GzipTransparentDecompression(t *testing.T) {
	dir := t.TempDir()
	payload := []byte("decompressed-qcow2")
	wrapped := gzipBytes(t, payload)
	wantDigest := sha256Hex(payload) // digest of *decompressed* payload

	res := &stubResolver{payload: wrapped}
	c, err := New(dir, res)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := c.Get(context.Background(), infrav1.BaseImageSource{
		Type:  infrav1.BootArtifactsSourceHTTPS,
		HTTPS: &infrav1.HTTPSBaseImageSource{URL: "https://example.test/x.qcow2.gz"},
	}, nil, wantDigest)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SHA256 != wantDigest {
		t.Errorf("digest: got %s, want %s", got.SHA256, wantDigest)
	}
	body, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatalf("read cached file: %v", err)
	}
	if !bytes.Equal(body, payload) {
		t.Errorf("cached payload mismatch: got %q, want %q", body, payload)
	}
}

func TestCache_Get_DigestMismatchRejected(t *testing.T) {
	dir := t.TempDir()
	res := &stubResolver{payload: []byte("xx")}
	c, err := New(dir, res)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Get(context.Background(), infrav1.BaseImageSource{
		Type:  infrav1.BootArtifactsSourceHTTPS,
		HTTPS: &infrav1.HTTPSBaseImageSource{URL: "https://example.test/x.qcow2"},
	}, nil, "deadbeef")
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("expected sha256 mismatch error, got %v", err)
	}
	// Tmp file should have been cleaned up.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".part-") {
			t.Errorf("leftover temp file after mismatch: %s", e.Name())
		}
	}
}

// blockingResolver gates its Open() on a release channel so concurrent
// callers definitely overlap. Without this the first call can complete
// before later goroutines start, and singleflight has nothing to coalesce.
type blockingResolver struct {
	payload []byte
	gate    chan struct{}
	calls   atomic.Int64
}

func (b *blockingResolver) Open(ctx context.Context, _ infrav1.BaseImageSource, _ *Credentials) (io.ReadCloser, error) {
	b.calls.Add(1)
	select {
	case <-b.gate:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return io.NopCloser(bytes.NewReader(b.payload)), nil
}

func TestCache_Get_SingleFlight(t *testing.T) {
	dir := t.TempDir()
	res := &blockingResolver{payload: []byte("payload"), gate: make(chan struct{})}
	c, err := New(dir, res)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	src := infrav1.BaseImageSource{
		Type:  infrav1.BootArtifactsSourceHTTPS,
		HTTPS: &infrav1.HTTPSBaseImageSource{URL: "https://example.test/x.qcow2"},
	}

	const N = 16
	var wg sync.WaitGroup
	errs := make(chan error, N)
	for range N {
		wg.Go(func() {
			if _, err := c.Get(context.Background(), src, nil, ""); err != nil {
				errs <- err
			}
		})
	}
	// Let all goroutines reach singleflight.Do before releasing the
	// resolver. With a no-op resolver the first call would race ahead and
	// finish before slow goroutines registered, so we deliberately block
	// the resolver until everybody's queued behind the singleflight key.
	time.Sleep(50 * time.Millisecond)
	close(res.gate)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent Get failed: %v", err)
	}
	if got := res.calls.Load(); got != 1 {
		t.Errorf("expected single Open call (singleflight), got %d", got)
	}
}

func TestCache_Get_OpenError(t *testing.T) {
	dir := t.TempDir()
	res := &stubResolver{openErr: fmt.Errorf("boom")}
	c, err := New(dir, res)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Get(context.Background(), infrav1.BaseImageSource{
		Type:  infrav1.BootArtifactsSourceHTTPS,
		HTTPS: &infrav1.HTTPSBaseImageSource{URL: "https://example.test/x.qcow2"},
	}, nil, "")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected boom error, got %v", err)
	}
}

func TestMultiResolver_DispatchValidation(t *testing.T) {
	m := NewMultiResolver()
	_, err := m.Open(context.Background(), infrav1.BaseImageSource{Type: "Garbage"}, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("expected unsupported error, got %v", err)
	}
	_, err = m.Open(context.Background(), infrav1.BaseImageSource{Type: infrav1.BootArtifactsSourceHTTPS}, nil)
	if err == nil || !strings.Contains(err.Error(), "https is required") {
		t.Errorf("expected https-required error, got %v", err)
	}
	_, err = m.Open(context.Background(), infrav1.BaseImageSource{Type: infrav1.BootArtifactsSourceOCI}, nil)
	if err == nil || !strings.Contains(err.Error(), "oci is required") {
		t.Errorf("expected oci-required error, got %v", err)
	}
	_, err = m.Open(context.Background(), infrav1.BaseImageSource{Type: infrav1.BootArtifactsSourceS3}, nil)
	if err == nil || !strings.Contains(err.Error(), "s3 is required") {
		t.Errorf("expected s3-required error, got %v", err)
	}
}
