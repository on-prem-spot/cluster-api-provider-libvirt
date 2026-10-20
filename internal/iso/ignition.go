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
	"fmt"
	"os"

	"github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/filesystem/iso9660"
)

const (
	// isoBlockSize is the standard ISO9660 block size.
	isoBlockSize = 2048
	// minISOSize is the minimum ISO image size (2 MB).
	minISOSize = 2 * 1024 * 1024
	// isoOverheadBytes is overhead for ISO filesystem metadata.
	isoOverheadBytes = 256 * 1024
)

// BuildIgnitionISO creates an ISO9660 image containing the ignition configuration.
// The ISO has volume label "ignition" and contains /ignition/config.ign.
func (b *DiskfsBuilder) BuildIgnitionISO(ignitionData []byte) ([]byte, error) {
	tmpFile, err := os.CreateTemp("", "caplv-ignition-*.iso")
	if err != nil {
		return nil, fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	_ = os.Remove(tmpPath) // Remove so diskfs.Create can use O_EXCL
	defer func() { _ = os.Remove(tmpPath) }()

	// Calculate disk size: data + overhead, rounded up to block boundary, minimum 2MB.
	diskSize := max(int64(len(ignitionData))+isoOverheadBytes, minISOSize)
	diskSize = roundUpToBlock(diskSize, isoBlockSize)

	d, err := diskfs.Create(tmpPath, diskSize, diskfs.SectorSize(isoBlockSize))
	if err != nil {
		return nil, fmt.Errorf("creating disk image: %w", err)
	}

	fspec := disk.FilesystemSpec{
		Partition:   0,
		FSType:      filesystem.TypeISO9660,
		VolumeLabel: "ignition",
	}
	fs, err := d.CreateFilesystem(fspec)
	if err != nil {
		return nil, fmt.Errorf("creating ISO9660 filesystem: %w", err)
	}

	if err := fs.Mkdir("/ignition"); err != nil {
		return nil, fmt.Errorf("creating /ignition directory: %w", err)
	}

	configFile, err := fs.OpenFile("/ignition/config.ign", os.O_CREATE|os.O_RDWR)
	if err != nil {
		return nil, fmt.Errorf("creating config.ign: %w", err)
	}
	if _, err := configFile.Write(ignitionData); err != nil {
		return nil, fmt.Errorf("writing ignition data: %w", err)
	}

	iso, ok := fs.(*iso9660.FileSystem)
	if !ok {
		return nil, fmt.Errorf("unexpected filesystem type")
	}
	// As in BuildCloudInitISO: the label and Rock Ridge names are set here.
	if err := iso.Finalize(iso9660.FinalizeOptions{VolumeIdentifier: "ignition", RockRidge: true}); err != nil {
		return nil, fmt.Errorf("finalizing ISO: %w", err)
	}

	data, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("reading ISO file: %w", err)
	}

	return data, nil
}

func roundUpToBlock(size, blockSize int64) int64 {
	remainder := size % blockSize
	if remainder == 0 {
		return size
	}
	return size + blockSize - remainder
}
