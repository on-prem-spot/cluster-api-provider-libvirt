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

package controller

import "fmt"

// resolveSecretNamespace returns the namespace a Secret reference resolves to,
// confining it to the referencing object's own namespace.
//
// The controller holds cluster-wide secrets get/list/watch, so honouring an
// arbitrary spec-supplied namespace would let anyone who can write a namespaced
// LibvirtHost or LibvirtCluster borrow a Secret they have no RBAC to read —
// and, because the same specs also choose the endpoint the credential is sent
// to, exfiltrate it. An empty namespace means "my own namespace" (the CAPI
// convention); naming a different one is rejected rather than silently honoured.
func resolveSecretNamespace(refNamespace, ownerNamespace, fieldPath string) (string, error) {
	if refNamespace == "" || refNamespace == ownerNamespace {
		return ownerNamespace, nil
	}
	return "", fmt.Errorf(
		"%s: cross-namespace Secret reference to namespace %q is not permitted; "+
			"the Secret must live in %q alongside the resource that references it",
		fieldPath, refNamespace, ownerNamespace)
}
