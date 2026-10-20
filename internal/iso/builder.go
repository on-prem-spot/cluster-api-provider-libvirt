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

// Builder defines the interface for building ISO images.
type Builder interface {
	BuildIgnitionISO(ignitionData []byte) ([]byte, error)
	BuildCloudInitISO(userData, networkConfig []byte, instanceID, hostname, providerID string) ([]byte, error)
}

// DiskfsBuilder implements Builder using go-diskfs.
type DiskfsBuilder struct{}

// NewDiskfsBuilder creates a new DiskfsBuilder.
func NewDiskfsBuilder() *DiskfsBuilder { return &DiskfsBuilder{} }
