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

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

const testFingerprint = "SHA256:nThbg6kXUpJWGl7E1IGOCspRomTxdCARLviKw6E5SY8"

func testHost() *infrav1.LibvirtHost {
	return &infrav1.LibvirtHost{
		ObjectMeta: metav1.ObjectMeta{Name: "host-01", Namespace: "default"},
		Spec: infrav1.LibvirtHostSpec{
			URI:                "qemu+ssh://caplv@hv1.example.com/system",
			SecretRef:          &infrav1.SecretReference{Name: "hv1-ssh"},
			HostKeyFingerprint: testFingerprint,
			FirmwarePath:       "/usr/share/OVMF/OVMF_CODE.secboot.fd",
			NVRAMTemplatePath:  "/usr/share/OVMF/OVMF_VARS.fd",
		},
	}
}

func TestLibvirtHostValidateCreate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*infrav1.LibvirtHost)
		wantErr string
	}{
		{name: "valid"},
		{name: "session mode with port", mutate: func(h *infrav1.LibvirtHost) {
			h.Spec.URI = "qemu+ssh://caplv@hv1.example.com:2222/session"
		}},
		{name: "local URI", wantErr: "spec.uri", mutate: func(h *infrav1.LibvirtHost) {
			h.Spec.URI = "qemu:///system"
		}},
		{name: "unknown daemon path", wantErr: "spec.uri", mutate: func(h *infrav1.LibvirtHost) {
			h.Spec.URI = "qemu+ssh://caplv@hv1.example.com/systen"
		}},
		{name: "port out of range", wantErr: "spec.uri", mutate: func(h *infrav1.LibvirtHost) {
			h.Spec.URI = "qemu+ssh://caplv@hv1.example.com:70000/system"
		}},
		{name: "MD5 fingerprint", wantErr: "spec.hostKeyFingerprint", mutate: func(h *infrav1.LibvirtHost) {
			h.Spec.HostKeyFingerprint = "MD5:16:27:ac:a5:76:28:2d:36:63:1b:56:4d:eb:df:a6:48"
		}},
		{name: "missing secret", wantErr: "spec.secretRef.name", mutate: func(h *infrav1.LibvirtHost) {
			h.Spec.SecretRef = nil
		}},
		{name: "cross-namespace secret", wantErr: "spec.secretRef.namespace", mutate: func(h *infrav1.LibvirtHost) {
			h.Spec.SecretRef.Namespace = "kube-system"
		}},
		{name: "identity instead of secret", mutate: func(h *infrav1.LibvirtHost) {
			h.Spec.SecretRef = nil
			h.Spec.IdentityRef = &infrav1.LibvirtClusterIdentityReference{Name: "shared"}
		}},
		{name: "identity and secret", wantErr: "spec.identityRef", mutate: func(h *infrav1.LibvirtHost) {
			h.Spec.IdentityRef = &infrav1.LibvirtClusterIdentityReference{Name: "shared"}
		}},
		{name: "identity without name", wantErr: "spec.identityRef.name", mutate: func(h *infrav1.LibvirtHost) {
			h.Spec.SecretRef = nil
			h.Spec.IdentityRef = &infrav1.LibvirtClusterIdentityReference{}
		}},
		{name: "relative firmware path", wantErr: "spec.firmwarePath", mutate: func(h *infrav1.LibvirtHost) {
			h.Spec.FirmwarePath = "OVMF_CODE.fd"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obj := testHost()
			if tc.mutate != nil {
				tc.mutate(obj)
			}
			_, err := (&LibvirtHostCustomValidator{}).ValidateCreate(context.Background(), obj)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("expected an error on %s, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestLibvirtHostWarnsWithoutFingerprint(t *testing.T) {
	obj := testHost()
	obj.Spec.HostKeyFingerprint = ""
	warnings, err := (&LibvirtHostCustomValidator{}).ValidateCreate(context.Background(), obj)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "refuse to connect") {
		t.Errorf("expected a refusal warning, got %v", warnings)
	}
}

func TestLibvirtHostURIImmutableWhileMachinesPlaced(t *testing.T) {
	s := runtime.NewScheme()
	if err := infrav1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	old := testHost()
	moved := old.DeepCopy()
	moved.Spec.URI = "qemu+ssh://caplv@hv1-new.example.com/system"

	empty := &LibvirtHostCustomValidator{Client: fake.NewClientBuilder().WithScheme(s).Build()}
	if _, err := empty.ValidateUpdate(context.Background(), old, moved); err != nil {
		t.Errorf("URI change on an empty host was rejected: %v", err)
	}

	machine := &infrav1.LibvirtMachine{
		ObjectMeta: metav1.ObjectMeta{Name: "worker01", Namespace: "default"},
		Spec:       infrav1.LibvirtMachineSpec{HostRef: corev1.LocalObjectReference{Name: old.Name}},
	}
	busy := &LibvirtHostCustomValidator{Client: fake.NewClientBuilder().WithScheme(s).WithObjects(machine).Build()}
	_, err := busy.ValidateUpdate(context.Background(), old, moved)
	if err == nil || !strings.Contains(err.Error(), "worker01") {
		t.Errorf("expected URI change to be refused naming worker01, got %v", err)
	}
	other := old.DeepCopy()
	other.Spec.HealthCheckIntervalSeconds = 600
	if _, err := busy.ValidateUpdate(context.Background(), old, other); err != nil {
		t.Errorf("non-URI change on a busy host was rejected: %v", err)
	}
}

func TestLibvirtHostIdentityWarnings(t *testing.T) {
	s := runtime.NewScheme()
	if err := infrav1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	obj := testHost()
	obj.Spec.SecretRef = nil
	obj.Spec.IdentityRef = &infrav1.LibvirtClusterIdentityReference{Name: "shared"}
	identity := func(allowed *infrav1.AllowedNamespaces) *infrav1.LibvirtClusterIdentity {
		return &infrav1.LibvirtClusterIdentity{
			ObjectMeta: metav1.ObjectMeta{Name: "shared"},
			Spec:       infrav1.LibvirtClusterIdentitySpec{SecretName: "key", AllowedNamespaces: allowed},
		}
	}
	defaultNS := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default", Labels: map[string]string{"team": "a"}}}

	for _, tc := range []struct {
		name     string
		identity *infrav1.LibvirtClusterIdentity
		wantWarn string
	}{
		{name: "missing identity", wantWarn: "does not exist yet"},
		{name: "allowed", identity: identity(&infrav1.AllowedNamespaces{List: []string{"default"}})},
		{name: "allowed by selector", identity: identity(&infrav1.AllowedNamespaces{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"team": "a"}},
		})},
		{name: "not allowed", identity: identity(nil), wantWarn: "does not allow namespace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := fake.NewClientBuilder().WithScheme(s).WithObjects(defaultNS)
			if tc.identity != nil {
				b = b.WithObjects(tc.identity)
			}
			warnings, err := (&LibvirtHostCustomValidator{Client: b.Build()}).ValidateCreate(context.Background(), obj)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantWarn == "" {
				if len(warnings) != 0 {
					t.Errorf("unexpected warnings %v", warnings)
				}
				return
			}
			if len(warnings) != 1 || !strings.Contains(warnings[0], tc.wantWarn) {
				t.Errorf("got warnings %v, want one containing %q", warnings, tc.wantWarn)
			}
		})
	}
}
