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

package libvirt

import (
	"context"
	"io"
)

// DomainInfo holds basic information about a libvirt domain.
type DomainInfo struct {
	Name  string
	UUID  string
	State string // "running", "shutoff", "paused", "crashed"
}

// NodeInfo holds host hardware information from virsh nodeinfo.
type NodeInfo struct {
	CPUs     int32 // total CPU threads (cores * threads * sockets)
	MemoryKB int64 // total memory in KB
}

// Client defines the interface for interacting with a libvirt host.
type Client interface {
	Ping(ctx context.Context) error
	// VerifyHypervisor confirms the host can actually run the KVM domains CAPLV
	// defines — catching a partial libvirt install (daemon up, QEMU/KVM driver
	// missing) that Ping and GetNodeInfo would not surface.
	VerifyHypervisor(ctx context.Context) error
	// VerifySessionPrerequisites confirms host setup that session-mode
	// (qemu:///session) machines depend on but no virsh query covers: a
	// setuid qemu-bridge-helper and loginctl lingering for the service
	// account. Only meaningful when the client connects to a session daemon.
	VerifySessionPrerequisites(ctx context.Context) error
	GetNodeInfo(ctx context.Context) (*NodeInfo, error)
	DomainExists(ctx context.Context, name string) (bool, error)
	GetDomain(ctx context.Context, name string) (*DomainInfo, error)
	DefineDomain(ctx context.Context, xmlDef string) (*DomainInfo, error)
	StartDomain(ctx context.Context, name string) error
	DestroyDomain(ctx context.Context, name string) error
	UndefineDomain(ctx context.Context, name string) error
	PoolExists(ctx context.Context, name string) (bool, error)
	PoolIsActive(ctx context.Context, name string) (bool, error)
	StartPool(ctx context.Context, name string) error
	CreateTmpfsPool(ctx context.Context, name, path, size string) error
	DestroyPool(ctx context.Context, name string) error
	VolumeExists(ctx context.Context, pool, name string) (bool, error)
	CreateVolumeFromBackingStore(ctx context.Context, pool, name, backingPath string, sizeBytes int64) error
	CloneVolume(ctx context.Context, pool, sourceName, targetName string) error
	CreateVolume(ctx context.Context, pool, name string, sizeBytes int64) error
	UploadVolumeFromBytes(ctx context.Context, pool, name string, data []byte) error
	// UploadQcow2Volume streams qcow2 bytes from r to a new qcow2 volume in
	// pool named name. The volume's libvirt-side capacity is set to
	// virtualSizeBytes (the qcow2 header's virtual disk size, parsed by the
	// caller from the local cache file).
	UploadQcow2Volume(ctx context.Context, pool, name string, r io.Reader, virtualSizeBytes int64) error
	DeleteVolume(ctx context.Context, pool, name string) error
	GetVolumePath(ctx context.Context, pool, name string) (string, error)
	WriteRemoteFile(ctx context.Context, path string, data []byte) error
	RemoteFileExists(ctx context.Context, path string) (bool, error)
	DeleteRemoteFile(ctx context.Context, path string) error
	// RemoveRemoteDir removes an empty directory on the remote host; a
	// missing one is not an error.
	RemoveRemoteDir(ctx context.Context, path string) error
	Close() error
}
