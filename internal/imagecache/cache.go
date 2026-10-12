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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sync/singleflight"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

// MaxBaseImageBytes caps a single decompressed base image. The cache volume in
// the shipped manifest is a 20Gi emptyDir shared by every image, so an
// unbounded stream evicts the pod; 16 GiB leaves room while clearing any real
// RHCOS qcow2 by an order of magnitude.
const MaxBaseImageBytes = 16 << 30

// Cache is a content-addressed local-disk cache for base images. Fetches for
// the same logical artifact (keyed by source-spec digest) are coalesced via
// singleflight so 50 concurrent reconciles only download once.
type Cache struct {
	Dir      string
	Resolver Resolver

	sf singleflight.Group
}

// Entry is the result of a successful Get: the absolute path to the staged
// file in the cache directory and its sha256 hex digest.
type Entry struct {
	Path   string
	SHA256 string
}

// New creates a Cache rooted at dir. The directory is created if it does not
// exist.
func New(dir string, resolver Resolver) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create cache dir %s: %w", dir, err)
	}
	return &Cache{Dir: dir, Resolver: resolver}, nil
}

// Get returns a cache Entry for the given source. If the entry is already in
// the cache (and matches the expected sha256 when supplied), the cached file
// is reused. Otherwise the Resolver streams bytes to a temp file in Dir; gzip
// transport wrapping is removed during the stream; the sha256 is computed
// and verified; and the temp file is renamed into place atomically.
func (c *Cache) Get(ctx context.Context, src infrav1.BaseImageSource, creds *Credentials, expectedSHA256 string) (*Entry, error) {
	// Fast path: if the expected digest is supplied and a matching file is
	// already in the cache, skip the network entirely.
	if expectedSHA256 != "" {
		path := c.pathFor(expectedSHA256)
		if _, err := os.Stat(path); err == nil {
			return &Entry{Path: path, SHA256: strings.ToLower(expectedSHA256)}, nil
		}
	}

	key := sourceKey(src) + ":" + expectedSHA256
	v, err, _ := c.sf.Do(key, func() (any, error) {
		return c.fetch(ctx, src, creds, expectedSHA256)
	})
	if err != nil {
		return nil, err
	}
	return v.(*Entry), nil
}

func (c *Cache) fetch(ctx context.Context, src infrav1.BaseImageSource, creds *Credentials, expectedSHA256 string) (*Entry, error) {
	rc, err := c.Resolver.Open(ctx, src, creds)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()

	stream, err := maybeGunzipStream(rc)
	if err != nil {
		return nil, fmt.Errorf("decompress: %w", err)
	}

	tmp, err := os.CreateTemp(c.Dir, ".part-*")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}

	// Bounded: `stream` may be gzip-decompressed, so its length is chosen by
	// whoever serves the source, not by the Content-Length. Without a cap a
	// decompression bomb fills the cache volume and gets the manager evicted.
	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(tmp, hasher), io.LimitReader(stream, MaxBaseImageBytes+1))
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("stream to cache: %w", err)
	}
	if written > MaxBaseImageBytes {
		cleanup()
		return nil, fmt.Errorf("base image exceeds the %d byte limit", int64(MaxBaseImageBytes))
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("close temp file: %w", err)
	}

	digest := hex.EncodeToString(hasher.Sum(nil))
	if expectedSHA256 != "" && !strings.EqualFold(digest, expectedSHA256) {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("sha256 mismatch: got %s, expected %s", digest, expectedSHA256)
	}

	finalPath := c.pathFor(digest)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("rename into cache: %w", err)
	}
	return &Entry{Path: finalPath, SHA256: digest}, nil
}

func (c *Cache) pathFor(sha string) string {
	return filepath.Join(c.Dir, strings.ToLower(sha))
}

// sourceKey returns a stable string identifying the logical artifact, used
// only as a singleflight key so concurrent fetchers coalesce. Two callers
// disagreeing on the source spec just race independently — they each write
// a temp file, the sha256 verification rejects wrong content.
func sourceKey(src infrav1.BaseImageSource) string {
	var b strings.Builder
	b.WriteString(string(src.Type))
	b.WriteByte('|')
	if src.HTTPS != nil {
		b.WriteString(src.HTTPS.URL)
	}
	if src.OCI != nil {
		b.WriteString(src.OCI.Reference)
		b.WriteByte('|')
		b.WriteString(src.OCI.BlobTitle)
	}
	if src.S3 != nil {
		b.WriteString(src.S3.Endpoint)
		b.WriteByte('/')
		b.WriteString(src.S3.Bucket)
		b.WriteByte('/')
		b.WriteString(src.S3.Key)
	}
	return b.String()
}
