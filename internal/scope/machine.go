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

package scope

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

// MachineScopeParams groups the parameters required to create a MachineScope.
type MachineScopeParams struct {
	Client         client.Client
	Cluster        *clusterv1.Cluster
	Machine        *clusterv1.Machine
	LibvirtCluster *infrav1.LibvirtCluster
	LibvirtMachine *infrav1.LibvirtMachine
	LibvirtHost    *infrav1.LibvirtHost
}

// MachineScope gathers all context needed for a LibvirtMachine reconciliation.
type MachineScope struct {
	client         client.Client
	Cluster        *clusterv1.Cluster
	Machine        *clusterv1.Machine
	LibvirtCluster *infrav1.LibvirtCluster
	LibvirtMachine *infrav1.LibvirtMachine
	LibvirtHost    *infrav1.LibvirtHost
}

// NewMachineScope creates a new MachineScope, validating that all required parameters
// are provided.
func NewMachineScope(params MachineScopeParams) (*MachineScope, error) {
	if params.Client == nil {
		return nil, fmt.Errorf("client is required")
	}
	if params.Cluster == nil {
		return nil, fmt.Errorf("cluster is required")
	}
	if params.Machine == nil {
		return nil, fmt.Errorf("machine is required")
	}
	if params.LibvirtCluster == nil {
		return nil, fmt.Errorf("libvirt cluster is required")
	}
	if params.LibvirtMachine == nil {
		return nil, fmt.Errorf("libvirt machine is required")
	}
	if params.LibvirtHost == nil {
		return nil, fmt.Errorf("libvirt host is required")
	}

	return &MachineScope{
		client:         params.Client,
		Cluster:        params.Cluster,
		Machine:        params.Machine,
		LibvirtCluster: params.LibvirtCluster,
		LibvirtMachine: params.LibvirtMachine,
		LibvirtHost:    params.LibvirtHost,
	}, nil
}

// ArtifactBaseName returns the deterministic artifact base name.
// Format: <namespace>-<clusterName>-<machineName>
func (s *MachineScope) ArtifactBaseName() string {
	return fmt.Sprintf("%s-%s-%s",
		s.LibvirtMachine.Namespace,
		s.Cluster.Name,
		s.LibvirtMachine.Name)
}

// DomainName returns the libvirt domain name for this machine.
func (s *MachineScope) DomainName() string { return s.ArtifactBaseName() }

// maxHostnameLength is the longest hostname, and so Node name, a kubelet
// can register: the kubernetes.io/hostname label value is capped at 63.
const maxHostnameLength = 63

// Hostname returns the guest hostname, which the kubelet registers as the
// Node name. It is the domain name when that fits in maxHostnameLength,
// and otherwise a prefix of it with a hash of the whole name appended, so
// distinct machines keep distinct hostnames.
func (s *MachineScope) Hostname() string {
	return HostnameFor(s.DomainName())
}

// HostnameFor returns the guest hostname for a domain name; see Hostname.
func HostnameFor(domainName string) string {
	if len(domainName) <= maxHostnameLength {
		return domainName
	}
	sum := sha256.Sum256([]byte(domainName))
	suffix := hex.EncodeToString(sum[:])[:8]
	prefix := strings.TrimRight(domainName[:maxHostnameLength-len(suffix)-1], "-")
	return prefix + "-" + suffix
}

// RootDiskVolumeName returns the name of the root disk volume.
func (s *MachineScope) RootDiskVolumeName() string {
	return s.ArtifactBaseName() + "-root.qcow2"
}

// BootstrapISOName returns the name of the bootstrap ISO volume.
func (s *MachineScope) BootstrapISOName() string {
	return s.ArtifactBaseName() + "-bootstrap.iso"
}

// NVRAMPath returns the path to the NVRAM file on the host.
func (s *MachineScope) NVRAMPath() string {
	return fmt.Sprintf("/var/lib/libvirt/qemu/nvram/%s_VARS.fd", s.ArtifactBaseName())
}

// ProviderID returns the provider ID for this machine.
func (s *MachineScope) ProviderID() string {
	return fmt.Sprintf("libvirt:///%s/%s", s.LibvirtHost.Name, s.DomainName())
}

// IgnitionFilePath returns the path for the ignition JSON file on the host.
func (s *MachineScope) IgnitionFilePath() string {
	return fmt.Sprintf("/run/caplv/%s/ignition.json", s.ArtifactBaseName())
}

// EphemeralPoolName returns the name for the per-machine tmpfs storage pool.
func (s *MachineScope) EphemeralPoolName() string {
	return s.ArtifactBaseName() + "-pool"
}

// EphemeralPoolPath returns the mount path for the per-machine tmpfs.
func (s *MachineScope) EphemeralPoolPath() string {
	return fmt.Sprintf("/run/caplv/%s", s.ArtifactBaseName())
}

// GetBootstrapData reads the bootstrap data secret referenced by the CAPI Machine.
func (s *MachineScope) GetBootstrapData(ctx context.Context) ([]byte, error) {
	if s.Machine.Spec.Bootstrap.DataSecretName == nil {
		return nil, fmt.Errorf("bootstrap data secret not yet available")
	}
	secret := &corev1.Secret{}
	key := types.NamespacedName{
		Namespace: s.Machine.Namespace,
		Name:      *s.Machine.Spec.Bootstrap.DataSecretName,
	}
	if err := s.client.Get(ctx, key, secret); err != nil {
		return nil, fmt.Errorf("failed to get bootstrap secret: %w", err)
	}
	data, ok := secret.Data["value"]
	if !ok {
		return nil, fmt.Errorf("bootstrap secret missing 'value' key")
	}
	return data, nil
}

// GetAddresses extracts IP addresses from the LibvirtMachine spec, stripping
// any CIDR prefix length.
func (s *MachineScope) GetAddresses() []infrav1.MachineAddress {
	addresses := make([]infrav1.MachineAddress, 0, len(s.LibvirtMachine.Spec.Network.Addresses))
	for _, addr := range s.LibvirtMachine.Spec.Network.Addresses {
		ip := addr
		if idx := strings.Index(addr, "/"); idx > 0 {
			ip = addr[:idx]
		}
		addresses = append(addresses, infrav1.MachineAddress{
			Type:    infrav1.MachineInternalIP,
			Address: ip,
		})
	}
	return addresses
}
