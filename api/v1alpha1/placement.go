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

package v1alpha1

// AssignedHostName returns the name of the LibvirtHost the machine runs on:
// spec.hostRef when the machine is pinned, otherwise the host claimed for it
// as recorded in status.hostRef. It is empty while no host has been claimed.
func (m *LibvirtMachine) AssignedHostName() string {
	if m.Spec.HostRef != nil {
		return m.Spec.HostRef.Name
	}
	if m.Status.HostRef != nil {
		return m.Status.HostRef.Name
	}
	return ""
}

// EffectiveNetwork returns the machine's network configuration with each
// field it leaves unset taken from its host's spec.machineNetwork. host may
// be nil. The result is a copy; neither argument is modified.
func EffectiveNetwork(machine NetworkSpec, host *MachineNetworkSpec) NetworkSpec {
	net := *machine.DeepCopy()
	if host == nil {
		return net
	}
	if net.Type == "" {
		net.Type = host.Type
	}
	if net.Name == "" {
		net.Name = host.Name
	}
	if len(net.Addresses) == 0 {
		net.Addresses = append([]string(nil), host.Addresses...)
	}
	if net.Gateway == "" {
		net.Gateway = host.Gateway
	}
	if net.DNS == nil && host.DNS != nil {
		net.DNS = host.DNS.DeepCopy()
	}
	return net
}
