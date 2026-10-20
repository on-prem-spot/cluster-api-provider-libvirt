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
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

const testIdentityNamespace = "caplv-system"

func identityHost(namespace string) *infrav1.LibvirtHost {
	return &infrav1.LibvirtHost{
		ObjectMeta: metav1.ObjectMeta{Name: "host1", Namespace: namespace},
		Spec: infrav1.LibvirtHostSpec{
			URI:         "qemu+ssh://caplv@host/system",
			IdentityRef: &infrav1.LibvirtClusterIdentityReference{Name: "shared"},
		},
	}
}

func identity(allowed *infrav1.AllowedNamespaces) *infrav1.LibvirtClusterIdentity {
	return &infrav1.LibvirtClusterIdentity{
		ObjectMeta: metav1.ObjectMeta{Name: "shared"},
		Spec:       infrav1.LibvirtClusterIdentitySpec{SecretName: "shared-key", AllowedNamespaces: allowed},
	}
}

func TestHostSSHSecret(t *testing.T) {
	identitySecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "shared-key", Namespace: testIdentityNamespace},
		Data:       map[string][]byte{"ssh-privatekey": []byte("identity-key")},
	}
	hostSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "ssh-key", Namespace: "team-a"},
		Data:       map[string][]byte{"ssh-privatekey": []byte("host-key")},
	}
	teamA := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "team-a", Labels: map[string]string{"tier": "gold"}}}
	teamB := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "team-b"}}

	secretRefHost := identityHost("team-a")
	secretRefHost.Spec.IdentityRef = nil
	secretRefHost.Spec.SecretRef = &infrav1.SecretReference{Name: "ssh-key"}

	neitherHost := identityHost("team-a")
	neitherHost.Spec.IdentityRef = nil

	for _, tc := range []struct {
		name              string
		host              *infrav1.LibvirtHost
		identity          *infrav1.LibvirtClusterIdentity
		identityNamespace string
		wantKey           string
		wantErr           string
	}{
		{name: "secretRef", host: secretRefHost, wantKey: "host-key"},
		{name: "neither set", host: neitherHost, wantErr: "neither spec.secretRef nor spec.identityRef"},
		{
			name: "identity allows every namespace", host: identityHost("team-a"),
			identity: identity(&infrav1.AllowedNamespaces{}), wantKey: "identity-key",
		},
		{
			name: "identity allows by list", host: identityHost("team-a"),
			identity: identity(&infrav1.AllowedNamespaces{List: []string{"team-a"}}), wantKey: "identity-key",
		},
		{
			name: "identity allows by selector", host: identityHost("team-a"),
			identity: identity(&infrav1.AllowedNamespaces{Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"tier": "gold"},
			}}),
			wantKey: "identity-key",
		},
		{
			name: "selector does not match", host: identityHost("team-b"),
			identity: identity(&infrav1.AllowedNamespaces{Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"tier": "gold"},
			}}),
			wantErr: `does not allow namespace "team-b"`,
		},
		{
			name: "namespace not in list", host: identityHost("team-b"),
			identity: identity(&infrav1.AllowedNamespaces{List: []string{"team-a"}}),
			wantErr:  `does not allow namespace "team-b"`,
		},
		{
			name: "allowedNamespaces unset allows nothing", host: identityHost("team-a"),
			identity: identity(nil), wantErr: `does not allow namespace "team-a"`,
		},
		{
			name: "identity missing", host: identityHost("team-a"),
			wantErr: "failed to get LibvirtClusterIdentity shared",
		},
		{
			name: "controller namespace unknown", host: identityHost("team-a"),
			identity: identity(&infrav1.AllowedNamespaces{}), identityNamespace: "-",
			wantErr: "controller's namespace is unknown",
		},
		{
			// The identity's Secret is looked up in the controller's
			// namespace only, never the host's, even when the host's
			// namespace has a Secret of that name.
			name: "identity secret only in controller namespace", host: identityHost("team-a"),
			identity: identity(&infrav1.AllowedNamespaces{}), identityNamespace: "elsewhere",
			wantErr: "failed to get SSH secret elsewhere/shared-key",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objs := []client.Object{identitySecret, hostSecret, teamA, teamB}
			if tc.identity != nil {
				objs = append(objs, tc.identity)
			}
			c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(objs...).Build()
			identityNS := testIdentityNamespace
			switch tc.identityNamespace {
			case "":
			case "-":
				identityNS = ""
			default:
				identityNS = tc.identityNamespace
			}

			secret, err := hostSSHSecret(context.Background(), c, tc.host, identityNS)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("got error %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := string(secret.Data["ssh-privatekey"]); got != tc.wantKey {
				t.Errorf("got key %q, want %q", got, tc.wantKey)
			}
		})
	}
}

// A host whose identity does not allow its namespace reports that in its
// Ready condition rather than connecting.
func TestHostReconcile_IdentityNotAllowed_SetsNotReady(t *testing.T) {
	s := testScheme(t)
	host := identityHost("team-a")
	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(host, identity(&infrav1.AllowedNamespaces{List: []string{"team-b"}})).
		WithStatusSubresource(host).Build()
	r := &LibvirtHostReconciler{Client: c, Scheme: s, IdentityNamespace: testIdentityNamespace}

	key := types.NamespacedName{Name: "host1", Namespace: "team-a"}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	updated := &infrav1.LibvirtHost{}
	if err := c.Get(context.Background(), key, updated); err != nil {
		t.Fatalf("get host: %v", err)
	}
	if updated.Status.Ready {
		t.Error("expected Ready=false")
	}
	if len(updated.Status.Conditions) == 0 || !strings.Contains(updated.Status.Conditions[0].Message, "does not allow namespace") {
		t.Errorf("expected a condition naming the namespace refusal, got %+v", updated.Status.Conditions)
	}
}

func TestIdentityToLibvirtHosts(t *testing.T) {
	s := testScheme(t)
	other := identityHost("team-b")
	other.Spec.IdentityRef.Name = "other"
	secretHost := identityHost("team-c")
	secretHost.Spec.IdentityRef = nil
	secretHost.Spec.SecretRef = &infrav1.SecretReference{Name: "ssh-key"}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(identityHost("team-a"), other, secretHost).Build()
	r := &LibvirtHostReconciler{Client: c, Scheme: s}

	got := r.identityToLibvirtHosts(context.Background(), identity(nil))
	if len(got) != 1 || got[0].Namespace != "team-a" || got[0].Name != "host1" {
		t.Errorf("got %v, want only team-a/host1", got)
	}
}

// Resolving an identity makes it an owner of its Secret, once, for
// clusterctl move.
func TestHostSSHSecret_IdentityOwnsSecret(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "shared-key", Namespace: testIdentityNamespace},
		Data:       map[string][]byte{"ssh-privatekey": []byte("identity-key")},
	}
	id := identity(&infrav1.AllowedNamespaces{})
	id.UID = "identity-uid"
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(secret, id).Build()

	for range 2 {
		if _, err := hostSSHSecret(context.Background(), c, identityHost("team-a"), testIdentityNamespace); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	got := &corev1.Secret{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(secret), got); err != nil {
		t.Fatalf("get secret: %v", err)
	}
	refs := got.OwnerReferences
	if len(refs) != 1 || refs[0].Kind != "LibvirtClusterIdentity" || refs[0].Name != "shared" || refs[0].UID != "identity-uid" {
		t.Fatalf("got owner references %+v, want one to LibvirtClusterIdentity shared", refs)
	}
	if refs[0].Controller != nil && *refs[0].Controller {
		t.Error("owner reference should not be a controller reference")
	}
}
