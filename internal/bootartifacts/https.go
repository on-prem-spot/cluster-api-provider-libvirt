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
	"fmt"
	"net/http"
	"strings"
	"time"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

const httpsFetchTimeout = 5 * time.Minute

// HTTPSResolver fetches artifacts over HTTPS. Plain http is rejected so
// kernel/initramfs cannot be tampered with in transit.
type HTTPSResolver struct {
	Client *http.Client
}

// NewHTTPSResolver returns an HTTPSResolver using a default http.Client with
// a fetch timeout sized for cold caches of multi-MB initramfs payloads.
func NewHTTPSResolver() *HTTPSResolver {
	return &HTTPSResolver{
		Client: &http.Client{Timeout: httpsFetchTimeout},
	}
}

// Resolve fetches both the kernel and initramfs, computes their sha256
// digests, and verifies any user-supplied digests.
func (r *HTTPSResolver) Resolve(ctx context.Context, src infrav1.BootArtifactsSource, _ *Credentials) (*Artifacts, error) {
	if src.HTTPS == nil {
		return nil, fmt.Errorf("https source not set")
	}
	spec := src.HTTPS

	kernelBytes, kernelDigest, err := r.fetchOne(ctx, spec.KernelURL, spec.KernelSHA256)
	if err != nil {
		return nil, fmt.Errorf("kernel: %w", err)
	}
	initramfsBytes, initramfsDigest, err := r.fetchOne(ctx, spec.InitramfsURL, spec.InitramfsSHA256)
	if err != nil {
		return nil, fmt.Errorf("initramfs: %w", err)
	}

	return &Artifacts{
		KernelBytes:     kernelBytes,
		KernelSHA256:    kernelDigest,
		InitramfsBytes:  initramfsBytes,
		InitramfsSHA256: initramfsDigest,
	}, nil
}

func (r *HTTPSResolver) fetchOne(ctx context.Context, url, expectedDigest string) ([]byte, string, error) {
	if !strings.HasPrefix(strings.ToLower(url), "https://") {
		return nil, "", fmt.Errorf("only https:// URLs are supported, got %q", url)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("build request: %w", err)
	}
	resp, err := r.Client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("get %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("get %s: unexpected status %d", url, resp.StatusCode)
	}

	raw, err := readLimited(resp.Body, MaxArtifactBytes, "response body from "+url)
	if err != nil {
		return nil, "", fmt.Errorf("read %s: %w", url, err)
	}
	body, err := decompressIfGzip(raw)
	if err != nil {
		return nil, "", fmt.Errorf("decompress %s: %w", url, err)
	}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])

	if expectedDigest != "" && !strings.EqualFold(digest, expectedDigest) {
		return nil, "", fmt.Errorf("sha256 mismatch for %s: got %s, expected %s", url, digest, expectedDigest)
	}
	return body, digest, nil
}
