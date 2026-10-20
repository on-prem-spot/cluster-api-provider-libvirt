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

package iso

import (
	"bytes"
	"strings"
	"testing"
)

// assertVolumeID checks the volume identifier in the ISO9660 primary volume
// descriptor (sector 16, bytes 40-71, padded), which is what blkid and
// cloud-init's ds-identify report as the label.
func assertVolumeID(t *testing.T, data []byte, want string) {
	t.Helper()
	const pvd = 16 * 2048
	if len(data) < pvd+72 {
		t.Fatalf("ISO is too short (%d bytes) to hold a primary volume descriptor", len(data))
	}
	if got := strings.TrimRight(string(data[pvd+40:pvd+72]), " \x00"); got != want {
		t.Errorf("volume identifier is %q, want %q", got, want)
	}
}

// assertFileNames checks that each name is stored verbatim, as a Rock Ridge
// name. Plain ISO9660 names are upper case 8.3, so "user-data" would be
// stored as "USER_DAT" and guests would not find it.
func assertFileNames(t *testing.T, data []byte, names ...string) {
	t.Helper()
	for _, name := range names {
		if !bytes.Contains(data, []byte(name)) {
			t.Errorf("file name %q is not stored in the ISO; is Rock Ridge enabled?", name)
		}
	}
}
