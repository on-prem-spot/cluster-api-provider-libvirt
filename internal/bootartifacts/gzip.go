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
)

// decompressIfGzip returns raw unchanged if it does not start with the gzip
// magic bytes (0x1f 0x8b); otherwise it decompresses one layer of gzip and
// returns the payload. A raw kernel bzImage starts with "MZ" and a raw cpio
// initramfs starts with "070701", so the magic-byte sniff has no false
// positives on the artifacts we actually fetch.
//
// We decompress at most one layer. If Artifactory or a similar mirror wraps
// an already-gzipped initramfs in a second gzip for transport, the inner
// gzip is left intact and the kernel's own decompressor handles it at boot.
func decompressIfGzip(raw []byte) ([]byte, error) {
	if len(raw) < 2 || raw[0] != 0x1f || raw[1] != 0x8b {
		return raw, nil
	}
	r, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	// Bounded: the expansion ratio of the compressed input is attacker-chosen.
	return readLimited(r, MaxArtifactBytes, "gzip-decompressed artifact")
}
