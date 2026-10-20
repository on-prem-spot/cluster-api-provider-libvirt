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

// BuildCloudInitISO creates an ISO9660 NoCloud image containing cloud-init data.
// The ISO has volume label "cidata" and contains /user-data and /meta-data.
func (b *DiskfsBuilder) BuildCloudInitISO(userData []byte, instanceID, hostname string) ([]byte, error) {
	metaData := fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n", instanceID, hostname)

	tmpFile, err := os.CreateTemp("", "caplv-cloudinit-*.iso")
	if err != nil {
		return nil, fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	_ = os.Remove(tmpPath) // Remove so diskfs.Create can use O_EXCL
	defer func() { _ = os.Remove(tmpPath) }()

	// Calculate disk size: data + metadata + overhead, rounded up to block boundary, minimum 2MB.
	diskSize := max(int64(len(userData))+int64(len(metaData))+isoOverheadBytes, minISOSize)
	diskSize = roundUpToBlock(diskSize, isoBlockSize)

	d, err := diskfs.Create(tmpPath, diskSize, diskfs.SectorSize(isoBlockSize))
	if err != nil {
		return nil, fmt.Errorf("creating disk image: %w", err)
	}

	fspec := disk.FilesystemSpec{
		Partition:   0,
		FSType:      filesystem.TypeISO9660,
		VolumeLabel: "cidata",
	}
	fs, err := d.CreateFilesystem(fspec)
	if err != nil {
		return nil, fmt.Errorf("creating ISO9660 filesystem: %w", err)
	}

	// Write /user-data.
	userDataFile, err := fs.OpenFile("/user-data", os.O_CREATE|os.O_RDWR)
	if err != nil {
		return nil, fmt.Errorf("creating user-data: %w", err)
	}
	if _, err := userDataFile.Write(userData); err != nil {
		return nil, fmt.Errorf("writing user-data: %w", err)
	}

	// Write /meta-data.
	metaDataFile, err := fs.OpenFile("/meta-data", os.O_CREATE|os.O_RDWR)
	if err != nil {
		return nil, fmt.Errorf("creating meta-data: %w", err)
	}
	if _, err := metaDataFile.Write([]byte(metaData)); err != nil {
		return nil, fmt.Errorf("writing meta-data: %w", err)
	}

	iso, ok := fs.(*iso9660.FileSystem)
	if !ok {
		return nil, fmt.Errorf("unexpected filesystem type")
	}
	// go-diskfs ignores FilesystemSpec.VolumeLabel for ISO9660; the label
	// cloud-init looks for is set here. Rock Ridge keeps the file names: plain
	// ISO9660 would store them as USER_DAT and META_DAT.
	if err := iso.Finalize(iso9660.FinalizeOptions{VolumeIdentifier: "cidata", RockRidge: true}); err != nil {
		return nil, fmt.Errorf("finalizing ISO: %w", err)
	}

	data, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("reading ISO file: %w", err)
	}

	return data, nil
}
