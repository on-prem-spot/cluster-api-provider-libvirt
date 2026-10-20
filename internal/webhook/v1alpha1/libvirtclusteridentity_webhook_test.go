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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrastructurev1alpha1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

// These run against envtest, so they cover the CRD schema and the
// secretRef/identityRef CEL rule on LibvirtHost as well as the webhook.
var _ = Describe("LibvirtClusterIdentity Webhook", func() {
	newIdentity := func(name string) *infrastructurev1alpha1.LibvirtClusterIdentity {
		return &infrastructurev1alpha1.LibvirtClusterIdentity{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: infrastructurev1alpha1.LibvirtClusterIdentitySpec{
				SecretName:        "caplv-ssh",
				AllowedNamespaces: &infrastructurev1alpha1.AllowedNamespaces{},
			},
		}
	}
	deleteAfter := func(obj client.Object) {
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, obj))).To(Succeed()) })
	}

	It("admits a valid identity and allows rotating its Secret", func() {
		obj := newIdentity("valid")
		Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		deleteAfter(obj)

		obj.Spec.SecretName = "caplv-ssh-rotated"
		Expect(k8sClient.Update(ctx, obj)).To(Succeed())
	})

	It("rejects a secretName that is not a valid Secret name", func() {
		obj := newIdentity("bad-secret")
		obj.Spec.SecretName = "Not_A_Name"
		Expect(k8sClient.Create(ctx, obj)).To(MatchError(ContainSubstring("spec.secretName")))
	})

	It("rejects an invalid namespace selector", func() {
		obj := newIdentity("bad-selector")
		obj.Spec.AllowedNamespaces.Selector = &metav1.LabelSelector{
			MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "team", Operator: "Near"}},
		}
		Expect(k8sClient.Create(ctx, obj)).To(MatchError(ContainSubstring("spec.allowedNamespaces.selector")))
	})

	It("rejects a namespace list entry that is not a namespace name", func() {
		obj := newIdentity("bad-list")
		obj.Spec.AllowedNamespaces.List = []string{"Team_A"}
		Expect(k8sClient.Create(ctx, obj)).To(MatchError(ContainSubstring("spec.allowedNamespaces.list[0]")))
	})

	Describe("LibvirtHost credential reference", func() {
		newHost := func(name string) *infrastructurev1alpha1.LibvirtHost {
			return &infrastructurev1alpha1.LibvirtHost{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec:       infrastructurev1alpha1.LibvirtHostSpec{URI: "qemu+ssh://caplv@hv.example.com/system"},
			}
		}

		It("admits an identityRef alone", func() {
			host := newHost("identity-only")
			host.Spec.IdentityRef = &infrastructurev1alpha1.LibvirtClusterIdentityReference{Name: "shared"}
			Expect(k8sClient.Create(ctx, host)).To(Succeed())
			deleteAfter(host)
		})

		It("rejects both secretRef and identityRef", func() {
			host := newHost("both")
			host.Spec.SecretRef = &infrastructurev1alpha1.SecretReference{Name: "ssh"}
			host.Spec.IdentityRef = &infrastructurev1alpha1.LibvirtClusterIdentityReference{Name: "shared"}
			Expect(k8sClient.Create(ctx, host)).To(MatchError(ContainSubstring("exactly one of secretRef and identityRef")))
		})

		It("rejects neither", func() {
			Expect(k8sClient.Create(ctx, newHost("neither"))).
				To(MatchError(ContainSubstring("exactly one of secretRef and identityRef")))
		})
	})
})
