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
	"encoding/xml"
	"fmt"
	"strings"
)

// dhcpNetwork is the part of a libvirt network definition that decides
// whether a machine's address needs a DHCP reservation.
type dhcpNetwork struct {
	IPs []struct {
		Family string `xml:"family,attr"`
		DHCP   *struct {
			Ranges []struct{} `xml:"range"`
			Hosts  []struct {
				MAC string `xml:"mac,attr"`
				IP  string `xml:"ip,attr"`
			} `xml:"host"`
		} `xml:"dhcp"`
	} `xml:"ip"`
}

// dhcpAction is what a network definition calls for to reserve ip for mac.
type dhcpAction int

const (
	// dhcpLeaveAlone: the network hands out IPv4 addresses of its own, or
	// already reserves ip for mac.
	dhcpLeaveAlone dhcpAction = iota
	// dhcpAdd: no reservation for mac yet.
	dhcpAdd
	// dhcpReplace: mac is reserved, but for another address.
	dhcpReplace
)

// dhcpReservationAction decides, from a network's XML, whether ip must be
// reserved for mac. Every update of a network's DHCP hosts makes libvirt
// remove and re-add the network's firewall rules, which disturbs the
// network and drops rules others inserted into libvirt's chains, so the
// network is changed only when it must be: when it gives a guest no IPv4
// address by DHCP of its own (it has no range), and the reservation is not
// already there.
func dhcpReservationAction(networkXML, mac, ip string) (dhcpAction, error) {
	var network dhcpNetwork
	if err := xml.Unmarshal([]byte(networkXML), &network); err != nil {
		return dhcpLeaveAlone, fmt.Errorf("parse network XML: %w", err)
	}
	action := dhcpAdd
	for _, addr := range network.IPs {
		if addr.Family != "" && addr.Family != "ipv4" || addr.DHCP == nil {
			continue
		}
		if len(addr.DHCP.Ranges) > 0 {
			return dhcpLeaveAlone, nil
		}
		for _, host := range addr.DHCP.Hosts {
			if strings.EqualFold(host.MAC, mac) {
				if host.IP == ip {
					return dhcpLeaveAlone, nil
				}
				action = dhcpReplace
			}
		}
	}
	return action, nil
}

// dhcpReservedFor reports whether a network's XML reserves an address for
// mac.
func dhcpReservedFor(networkXML, mac string) (bool, error) {
	var network dhcpNetwork
	if err := xml.Unmarshal([]byte(networkXML), &network); err != nil {
		return false, fmt.Errorf("parse network XML: %w", err)
	}
	for _, addr := range network.IPs {
		if addr.DHCP == nil {
			continue
		}
		for _, host := range addr.DHCP.Hosts {
			if strings.EqualFold(host.MAC, mac) {
				return true, nil
			}
		}
	}
	return false, nil
}
