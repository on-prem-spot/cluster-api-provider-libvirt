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

// MockClient implements Client for testing.
type MockClient struct {
	PingFn                         func(ctx context.Context) error
	VerifyHypervisorFn             func(ctx context.Context) error
	VerifySessionPrerequisitesFn   func(ctx context.Context) error
	GetNodeInfoFn                  func(ctx context.Context) (*NodeInfo, error)
	DomainExistsFn                 func(ctx context.Context, name string) (bool, error)
	GetDomainFn                    func(ctx context.Context, name string) (*DomainInfo, error)
	DefineDomainFn                 func(ctx context.Context, xmlDef string) (*DomainInfo, error)
	StartDomainFn                  func(ctx context.Context, name string) error
	DestroyDomainFn                func(ctx context.Context, name string) error
	UndefineDomainFn               func(ctx context.Context, name string) error
	PoolExistsFn                   func(ctx context.Context, name string) (bool, error)
	PoolIsActiveFn                 func(ctx context.Context, name string) (bool, error)
	StartPoolFn                    func(ctx context.Context, name string) error
	CreateTmpfsPoolFn              func(ctx context.Context, name, path, size string) error
	DestroyPoolFn                  func(ctx context.Context, name string) error
	VolumeExistsFn                 func(ctx context.Context, pool, name string) (bool, error)
	CreateVolumeFromBackingStoreFn func(ctx context.Context, pool, name, backingPath string, sizeBytes int64) error
	CloneVolumeFn                  func(ctx context.Context, pool, sourceName, targetName string) error
	CreateVolumeFn                 func(ctx context.Context, pool, name string, sizeBytes int64) error
	UploadVolumeFromBytesFn        func(ctx context.Context, pool, name string, data []byte) error
	UploadQcow2VolumeFn            func(ctx context.Context, pool, name string, r io.Reader, virtualSizeBytes int64) error
	DeleteVolumeFn                 func(ctx context.Context, pool, name string) error
	GetVolumePathFn                func(ctx context.Context, pool, name string) (string, error)
	WriteRemoteFileFn              func(ctx context.Context, path string, data []byte) error
	WriteRemoteFileFromFn          func(ctx context.Context, path string, r io.Reader) error
	RemoteFileExistsFn             func(ctx context.Context, path string) (bool, error)
	DeleteRemoteFileFn             func(ctx context.Context, path string) error
	RemoveRemoteDirFn              func(ctx context.Context, path string) error
	ReserveDHCPAddressFn           func(ctx context.Context, network, mac, ip, name string) error
	ReleaseDHCPAddressFn           func(ctx context.Context, network, mac string) error
	CloseFn                        func() error
}

// Ping delegates to PingFn or returns nil.
func (m *MockClient) Ping(ctx context.Context) error {
	if m.PingFn != nil {
		return m.PingFn(ctx)
	}
	return nil
}

// VerifyHypervisor delegates to VerifyHypervisorFn or returns nil.
func (m *MockClient) VerifyHypervisor(ctx context.Context) error {
	if m.VerifyHypervisorFn != nil {
		return m.VerifyHypervisorFn(ctx)
	}
	return nil
}

// VerifySessionPrerequisites delegates to VerifySessionPrerequisitesFn or returns nil.
func (m *MockClient) VerifySessionPrerequisites(ctx context.Context) error {
	if m.VerifySessionPrerequisitesFn != nil {
		return m.VerifySessionPrerequisitesFn(ctx)
	}
	return nil
}

// GetNodeInfo delegates to GetNodeInfoFn or returns empty NodeInfo.
func (m *MockClient) GetNodeInfo(ctx context.Context) (*NodeInfo, error) {
	if m.GetNodeInfoFn != nil {
		return m.GetNodeInfoFn(ctx)
	}
	return &NodeInfo{}, nil
}

// DomainExists delegates to DomainExistsFn or returns false, nil.
func (m *MockClient) DomainExists(ctx context.Context, name string) (bool, error) {
	if m.DomainExistsFn != nil {
		return m.DomainExistsFn(ctx, name)
	}
	return false, nil
}

// GetDomain delegates to GetDomainFn or returns an empty DomainInfo.
func (m *MockClient) GetDomain(ctx context.Context, name string) (*DomainInfo, error) {
	if m.GetDomainFn != nil {
		return m.GetDomainFn(ctx, name)
	}
	return &DomainInfo{}, nil
}

// DefineDomain delegates to DefineDomainFn or returns an empty DomainInfo.
func (m *MockClient) DefineDomain(ctx context.Context, xmlDef string) (*DomainInfo, error) {
	if m.DefineDomainFn != nil {
		return m.DefineDomainFn(ctx, xmlDef)
	}
	return &DomainInfo{}, nil
}

// StartDomain delegates to StartDomainFn or returns nil.
func (m *MockClient) StartDomain(ctx context.Context, name string) error {
	if m.StartDomainFn != nil {
		return m.StartDomainFn(ctx, name)
	}
	return nil
}

// DestroyDomain delegates to DestroyDomainFn or returns nil.
func (m *MockClient) DestroyDomain(ctx context.Context, name string) error {
	if m.DestroyDomainFn != nil {
		return m.DestroyDomainFn(ctx, name)
	}
	return nil
}

// UndefineDomain delegates to UndefineDomainFn or returns nil.
func (m *MockClient) UndefineDomain(ctx context.Context, name string) error {
	if m.UndefineDomainFn != nil {
		return m.UndefineDomainFn(ctx, name)
	}
	return nil
}

// PoolExists delegates to PoolExistsFn or returns false, nil.
func (m *MockClient) PoolExists(ctx context.Context, name string) (bool, error) {
	if m.PoolExistsFn != nil {
		return m.PoolExistsFn(ctx, name)
	}
	return false, nil
}

// PoolIsActive delegates to PoolIsActiveFn or returns true, nil.
func (m *MockClient) PoolIsActive(ctx context.Context, name string) (bool, error) {
	if m.PoolIsActiveFn != nil {
		return m.PoolIsActiveFn(ctx, name)
	}
	return true, nil
}

// StartPool delegates to StartPoolFn or returns nil.
func (m *MockClient) StartPool(ctx context.Context, name string) error {
	if m.StartPoolFn != nil {
		return m.StartPoolFn(ctx, name)
	}
	return nil
}

// CreateTmpfsPool delegates to CreateTmpfsPoolFn or returns nil.
func (m *MockClient) CreateTmpfsPool(ctx context.Context, name, path, size string) error {
	if m.CreateTmpfsPoolFn != nil {
		return m.CreateTmpfsPoolFn(ctx, name, path, size)
	}
	return nil
}

// DestroyPool delegates to DestroyPoolFn or returns nil.
func (m *MockClient) DestroyPool(ctx context.Context, name string) error {
	if m.DestroyPoolFn != nil {
		return m.DestroyPoolFn(ctx, name)
	}
	return nil
}

// VolumeExists delegates to VolumeExistsFn or returns false, nil.
func (m *MockClient) VolumeExists(ctx context.Context, pool, name string) (bool, error) {
	if m.VolumeExistsFn != nil {
		return m.VolumeExistsFn(ctx, pool, name)
	}
	return false, nil
}

// CreateVolumeFromBackingStore delegates to CreateVolumeFromBackingStoreFn or returns nil.
func (m *MockClient) CreateVolumeFromBackingStore(ctx context.Context, pool, name, backingPath string, sizeBytes int64) error {
	if m.CreateVolumeFromBackingStoreFn != nil {
		return m.CreateVolumeFromBackingStoreFn(ctx, pool, name, backingPath, sizeBytes)
	}
	return nil
}

// CloneVolume delegates to CloneVolumeFn or returns nil.
func (m *MockClient) CloneVolume(ctx context.Context, pool, sourceName, targetName string) error {
	if m.CloneVolumeFn != nil {
		return m.CloneVolumeFn(ctx, pool, sourceName, targetName)
	}
	return nil
}

// CreateVolume delegates to CreateVolumeFn or returns nil.
func (m *MockClient) CreateVolume(ctx context.Context, pool, name string, sizeBytes int64) error {
	if m.CreateVolumeFn != nil {
		return m.CreateVolumeFn(ctx, pool, name, sizeBytes)
	}
	return nil
}

// UploadVolumeFromBytes delegates to UploadVolumeFromBytesFn or returns nil.
func (m *MockClient) UploadVolumeFromBytes(ctx context.Context, pool, name string, data []byte) error {
	if m.UploadVolumeFromBytesFn != nil {
		return m.UploadVolumeFromBytesFn(ctx, pool, name, data)
	}
	return nil
}

// UploadQcow2Volume delegates to UploadQcow2VolumeFn or returns nil.
func (m *MockClient) UploadQcow2Volume(ctx context.Context, pool, name string, r io.Reader, virtualSizeBytes int64) error {
	if m.UploadQcow2VolumeFn != nil {
		return m.UploadQcow2VolumeFn(ctx, pool, name, r, virtualSizeBytes)
	}
	return nil
}

// DeleteVolume delegates to DeleteVolumeFn or returns nil.
func (m *MockClient) DeleteVolume(ctx context.Context, pool, name string) error {
	if m.DeleteVolumeFn != nil {
		return m.DeleteVolumeFn(ctx, pool, name)
	}
	return nil
}

// GetVolumePath delegates to GetVolumePathFn or returns empty string, nil.
func (m *MockClient) GetVolumePath(ctx context.Context, pool, name string) (string, error) {
	if m.GetVolumePathFn != nil {
		return m.GetVolumePathFn(ctx, pool, name)
	}
	return "", nil
}

// WriteRemoteFile delegates to WriteRemoteFileFn or returns nil.
func (m *MockClient) WriteRemoteFile(ctx context.Context, path string, data []byte) error {
	if m.WriteRemoteFileFn != nil {
		return m.WriteRemoteFileFn(ctx, path, data)
	}
	return nil
}

// WriteRemoteFileFrom delegates to WriteRemoteFileFromFn or returns nil.
func (m *MockClient) WriteRemoteFileFrom(ctx context.Context, path string, r io.Reader) error {
	if m.WriteRemoteFileFromFn != nil {
		return m.WriteRemoteFileFromFn(ctx, path, r)
	}
	return nil
}

// RemoteFileExists delegates to RemoteFileExistsFn or returns false, nil.
func (m *MockClient) RemoteFileExists(ctx context.Context, path string) (bool, error) {
	if m.RemoteFileExistsFn != nil {
		return m.RemoteFileExistsFn(ctx, path)
	}
	return false, nil
}

// DeleteRemoteFile delegates to DeleteRemoteFileFn or returns nil.
func (m *MockClient) DeleteRemoteFile(ctx context.Context, path string) error {
	if m.DeleteRemoteFileFn != nil {
		return m.DeleteRemoteFileFn(ctx, path)
	}
	return nil
}

// ReserveDHCPAddress delegates to ReserveDHCPAddressFn or returns nil.
func (m *MockClient) ReserveDHCPAddress(ctx context.Context, network, mac, ip, name string) error {
	if m.ReserveDHCPAddressFn != nil {
		return m.ReserveDHCPAddressFn(ctx, network, mac, ip, name)
	}
	return nil
}

// ReleaseDHCPAddress delegates to ReleaseDHCPAddressFn or returns nil.
func (m *MockClient) ReleaseDHCPAddress(ctx context.Context, network, mac string) error {
	if m.ReleaseDHCPAddressFn != nil {
		return m.ReleaseDHCPAddressFn(ctx, network, mac)
	}
	return nil
}

// RemoveRemoteDir delegates to RemoveRemoteDirFn or returns nil.
func (m *MockClient) RemoveRemoteDir(ctx context.Context, path string) error {
	if m.RemoveRemoteDirFn != nil {
		return m.RemoveRemoteDirFn(ctx, path)
	}
	return nil
}

// Close delegates to CloseFn or returns nil.
func (m *MockClient) Close() error {
	if m.CloseFn != nil {
		return m.CloseFn()
	}
	return nil
}
