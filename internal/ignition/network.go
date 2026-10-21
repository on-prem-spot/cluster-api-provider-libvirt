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

package ignition

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// NetworkConfig holds the parameters needed to generate static network
// configuration for an OpenShift worker node.
type NetworkConfig struct {
	// Addresses in CIDR notation, e.g. ["192.168.122.100/24"]
	Addresses []string
	// Gateway is the default route, e.g. "192.168.122.1"
	Gateway string
	// DNS servers, e.g. ["192.168.122.1"]
	DNSServers []string
	// DNS search domains
	DNSSearch []string
}

// InjectStaticNetwork merges static network configuration into an ignition
// config by writing an NMConnection file for the physical NIC (enp1s0).
//
// On OpenShift with OVN-Kubernetes, the configure-ovs.sh script runs at
// boot and migrates the physical NIC's configuration to an OVS bridge
// (br-ex). If the physical NIC already has a static IP, configure-ovs
// preserves it on br-ex. This is the same mechanism the Assisted Installer
// and agent-based installer use for bare metal nodes with static IPs.
//
// By setting method=manual on enp1s0 before any services start, we ensure:
//   - NetworkManager never requests a DHCP lease
//   - nodeip-configuration detects the correct static IP
//   - configure-ovs migrates the static config to br-ex
//   - CRI-O and kubelet get the right IP from the start
func InjectStaticNetwork(ignitionData []byte, net NetworkConfig) ([]byte, error) {
	if len(net.Addresses) == 0 {
		return ignitionData, nil
	}

	var config map[string]any
	if err := json.Unmarshal(ignitionData, &config); err != nil {
		return nil, fmt.Errorf("failed to parse ignition config: %w", err)
	}

	ignSection, ok := config["ignition"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("ignition config missing 'ignition' section")
	}

	version, _ := ignSection["version"].(string)
	var isV3 bool
	switch {
	case len(version) >= 1 && version[0] == '3':
		isV3 = true
	case len(version) >= 1 && version[0] == '2':
		isV3 = false
	default:
		return nil, fmt.Errorf("unsupported ignition version: %s", version)
	}

	// Default DNS to the gateway if not explicitly set.
	if len(net.DNSServers) == 0 && net.Gateway != "" {
		net.DNSServers = []string{net.Gateway}
	}

	nmConn := generateNMConnection(net)
	injectFile(config,
		"/etc/NetworkManager/system-connections/"+nicName+".nmconnection",
		"data:,"+url.PathEscape(nmConn), 0o600, isV3)

	return json.Marshal(config)
}

// nicName is the guest's name for its NIC: CAPLV attaches one virtio NIC,
// which a q35 machine puts on PCI bus 1.
const nicName = "enp1s0"

// generateNMConnection creates a NetworkManager keyfile that configures
// enp1s0 with a static IP. configure-ovs.sh will migrate this to br-ex.
func generateNMConnection(net NetworkConfig) string {
	var b strings.Builder

	b.WriteString("[connection]\n")
	b.WriteString("id=" + nicName + "\n")
	b.WriteString("type=ethernet\n")
	b.WriteString("interface-name=" + nicName + "\n")
	b.WriteString("autoconnect=true\n")
	b.WriteString("autoconnect-priority=1\n")
	b.WriteString("\n")

	b.WriteString("[ipv4]\n")
	b.WriteString("method=manual\n")
	for i, addr := range net.Addresses {
		b.WriteString(fmt.Sprintf("address%d=%s\n", i+1, addr))
	}
	if net.Gateway != "" {
		b.WriteString(fmt.Sprintf("gateway=%s\n", net.Gateway))
	}
	if len(net.DNSServers) > 0 {
		b.WriteString(fmt.Sprintf("dns=%s;\n", strings.Join(net.DNSServers, ";")))
	}
	if len(net.DNSSearch) > 0 {
		b.WriteString(fmt.Sprintf("dns-search=%s;\n", strings.Join(net.DNSSearch, ";")))
	}
	b.WriteString("\n")

	b.WriteString("[ipv6]\n")
	b.WriteString("method=disabled\n")

	return b.String()
}

// KernelNetworkArgs returns the dracut kernel arguments that give the
// initramfs the machine's static address, for direct kernel boot, where
// CAPLV writes the kernel command line.
//
// The keyfile from InjectStaticNetwork configures the NIC only once Ignition
// has run, which is enough when the whole config is delivered with the
// machine. A pointer config (such as OpenShift's worker-user-data) is fetched
// by Ignition from the initramfs, which then needs a network of its own; on a
// network without DHCP only these arguments give it one. The first IPv4
// address is enough to reach the machine config server; the keyfile takes
// over in the real system. It returns "" when there is no IPv4 address.
func KernelNetworkArgs(cfg NetworkConfig, hostname string) string {
	var ip net.IP
	var mask net.IPMask
	for _, addr := range cfg.Addresses {
		a, n, err := net.ParseCIDR(addr)
		if err == nil && a.To4() != nil {
			ip, mask = a.To4(), n.Mask
			break
		}
	}
	if ip == nil {
		return ""
	}

	// dracut: ip=<client-IP>:[<peer>]:<gateway-IP>:<netmask>:<client_hostname>:<interface>:none
	args := []string{
		"rd.neednet=1",
		fmt.Sprintf("ip=%s::%s:%s:%s:%s:none", ip, cfg.Gateway, net.IP(mask), hostname, nicName),
	}
	dnsServers := cfg.DNSServers
	if len(dnsServers) == 0 && cfg.Gateway != "" {
		dnsServers = []string{cfg.Gateway}
	}
	for _, server := range dnsServers {
		args = append(args, "nameserver="+server)
	}
	return strings.Join(args, " ")
}
