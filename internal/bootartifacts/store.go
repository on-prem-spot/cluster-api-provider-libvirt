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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// storeArtifact streams r into dir, decompressing it if it is gzip-wrapped and
// hashing it on the way, and leaves it at dir/<sha256 of the payload>. It
// returns that path and digest, or an error if expectedDigest is set and does
// not match. The payload never sits in memory: an RHCOS initramfs is over
// 100 MB, and the manager runs in 128 MiB.
func storeArtifact(dir string, r io.Reader, expectedDigest, what string) (string, string, error) {
	if dir == "" {
		return "", "", errors.New("no cache directory configured for boot artifacts")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", "", fmt.Errorf("create %s: %w", dir, err)
	}
	payload, err := gunzipIfGzip(newCappedReader(r, MaxArtifactBytes, what))
	if err != nil {
		return "", "", fmt.Errorf("decompress %s: %w", what, err)
	}

	tmp, err := os.CreateTemp(dir, ".partial-*")
	if err != nil {
		return "", "", fmt.Errorf("create temporary file in %s: %w", dir, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // a no-op once renamed
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(tmp, hash),
		newCappedReader(payload, MaxArtifactBytes, "decompressed "+what))
	if closeErr := tmp.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return "", "", fmt.Errorf("read %s: %w", what, copyErr)
	}

	digest := hex.EncodeToString(hash.Sum(nil))
	if expectedDigest != "" && !strings.EqualFold(digest, expectedDigest) {
		return "", "", fmt.Errorf("sha256 mismatch for %s: got %s, expected %s", what, digest, expectedDigest)
	}
	path := filepath.Join(dir, digest)
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", "", fmt.Errorf("store %s: %w", what, err)
	}
	return path, digest, nil
}

// cappedReader reads at most remaining bytes from r, and fails rather than
// truncating when r has more: the expansion ratio of a compressed input is
// attacker-chosen, and a pinned sha256 can only be checked at the end.
type cappedReader struct {
	r         io.Reader
	limit     int64
	remaining int64
	what      string
}

func newCappedReader(r io.Reader, limit int64, what string) *cappedReader {
	return &cappedReader{r: r, limit: limit, remaining: limit, what: what}
}

func (c *cappedReader) Read(p []byte) (int, error) {
	if c.remaining <= 0 {
		var probe [1]byte
		n, err := c.r.Read(probe[:])
		if n > 0 {
			return 0, fmt.Errorf("%s exceeds the %d byte limit", c.what, c.limit)
		}
		return 0, err
	}
	if int64(len(p)) > c.remaining {
		p = p[:c.remaining]
	}
	n, err := c.r.Read(p)
	c.remaining -= int64(n)
	return n, err
}
