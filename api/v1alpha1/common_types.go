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

// APIEndpoint, ObjectMeta and MachineAddress keep the schema v1alpha1 got from
// the CAPI v1.9 v1beta1 types of the same names, which it embedded until CAPLV
// moved to the CAPI v1beta2 Go API. The v1beta2 types add MinProperties=1,
// length limits and an address-type enum. APIEndpoint and ObjectMeta are
// non-pointer structs that serialize as {} when unset, so adopting those
// markers would reject objects the API has always accepted. Tightening them
// is left to the next API version (ADR 0003). Owning the types also keeps
// this package free of a CAPI dependency.

// APIEndpoint represents a reachable Kubernetes API endpoint.
type APIEndpoint struct {
	// host is the hostname on which the API server is serving.
	Host string `json:"host"`

	// port is the port on which the API server is serving.
	Port int32 `json:"port"`
}

// IsZero returns true if both host and port are zero values.
func (v APIEndpoint) IsZero() bool {
	return v.Host == "" && v.Port == 0
}

// ObjectMeta is the subset of metav1.ObjectMeta a template propagates to the
// objects created from it: labels and annotations.
type ObjectMeta struct {
	// labels is a map of string keys and values that can be used to organize and categorize
	// (scope and select) objects. May match selectors of replication controllers
	// and services.
	// More info: http://kubernetes.io/docs/user-guide/labels
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// annotations is an unstructured key value map stored with a resource that may be
	// set by external tools to store and retrieve arbitrary metadata. They are not
	// queryable and should be preserved when modifying objects.
	// More info: http://kubernetes.io/docs/user-guide/annotations
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// MachineAddressType is the type of a MachineAddress: one of Hostname,
// ExternalIP, InternalIP, ExternalDNS or InternalDNS.
type MachineAddressType string

// MachineInternalIP is the only address type CAPLV reports.
const MachineInternalIP MachineAddressType = "InternalIP"

// MachineAddress contains information for the node's address.
type MachineAddress struct {
	// type is the machine address type, one of Hostname, ExternalIP, InternalIP, ExternalDNS or InternalDNS.
	Type MachineAddressType `json:"type"`

	// address is the machine address.
	Address string `json:"address"`
}
