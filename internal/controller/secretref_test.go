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

import (
	"strings"
	"testing"
)

func TestResolveSecretNamespace_DefaultsToOwner(t *testing.T) {
	got, err := resolveSecretNamespace("", "tenant-a", "spec.secretRef.namespace")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "tenant-a" {
		t.Errorf("expected owner namespace 'tenant-a', got %q", got)
	}
}

func TestResolveSecretNamespace_AllowsExplicitOwnNamespace(t *testing.T) {
	got, err := resolveSecretNamespace("tenant-a", "tenant-a", "spec.secretRef.namespace")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "tenant-a" {
		t.Errorf("expected 'tenant-a', got %q", got)
	}
}

// A namespaced resource must not be able to name a Secret in someone else's
// namespace: the controller reads Secrets with cluster-wide RBAC, and the same
// specs choose the endpoint the credential is then sent to.
func TestResolveSecretNamespace_RejectsForeignNamespace(t *testing.T) {
	got, err := resolveSecretNamespace("openshift-config", "tenant-a", "spec.secretRef.namespace")
	if err == nil {
		t.Fatalf("expected cross-namespace reference to be rejected, got namespace %q", got)
	}
	if got != "" {
		t.Errorf("expected empty namespace on rejection, got %q", got)
	}
	// The message has to name both namespaces or an operator cannot act on it.
	for _, want := range []string{"openshift-config", "tenant-a", "spec.secretRef.namespace"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
