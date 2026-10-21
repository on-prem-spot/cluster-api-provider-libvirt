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
	"bufio"
	"compress/gzip"
	"io"
)

// gunzipIfGzip returns a reader of r's payload: r itself unless it starts
// with the gzip magic bytes (0x1f 0x8b), in which case one layer of gzip is
// decompressed as it is read. A raw kernel bzImage starts with "MZ" and a raw
// cpio initramfs starts with "070701", so the magic-byte sniff has no false
// positives on the artifacts we actually fetch.
//
// We decompress at most one layer. If Artifactory or a similar mirror wraps
// an already-gzipped initramfs in a second gzip for transport, the inner
// gzip is left intact and the kernel's own decompressor handles it at boot.
func gunzipIfGzip(r io.Reader) (io.Reader, error) {
	br := bufio.NewReader(r)
	magic, err := br.Peek(2)
	if err != nil || magic[0] != 0x1f || magic[1] != 0x8b {
		// Shorter than two bytes, or not gzip: the payload as it is.
		return br, nil
	}
	return gzip.NewReader(br)
}
