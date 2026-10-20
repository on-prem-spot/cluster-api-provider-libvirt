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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1validation "k8s.io/apimachinery/pkg/apis/meta/v1/validation"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	infrastructurev1alpha1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

var libvirtclusteridentitylog = logf.Log.WithName("libvirtclusteridentity-resource")

// SetupLibvirtClusterIdentityWebhookWithManager registers the webhook for LibvirtClusterIdentity in the manager.
func SetupLibvirtClusterIdentityWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &infrastructurev1alpha1.LibvirtClusterIdentity{}).
		WithValidator(&LibvirtClusterIdentityCustomValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-infrastructure-cluster-x-k8s-io-v1alpha1-libvirtclusteridentity,mutating=false,failurePolicy=fail,sideEffects=None,groups=infrastructure.cluster.x-k8s.io,resources=libvirtclusteridentities,verbs=create;update,versions=v1alpha1,name=vlibvirtclusteridentity-v1alpha1.kb.io,admissionReviewVersions=v1

// LibvirtClusterIdentityCustomValidator validates LibvirtClusterIdentity on
// create and update. Every field may change: rotating the Secret or
// narrowing allowedNamespaces is the point of the type.
type LibvirtClusterIdentityCustomValidator struct{}

// ValidateCreate implements webhook.CustomValidator.
func (v *LibvirtClusterIdentityCustomValidator) ValidateCreate(
	_ context.Context,
	obj *infrastructurev1alpha1.LibvirtClusterIdentity,
) (admission.Warnings, error) {
	libvirtclusteridentitylog.Info("Validation for LibvirtClusterIdentity upon creation", "name", obj.GetName())
	return validateIdentity(obj)
}

// ValidateUpdate implements webhook.CustomValidator.
func (v *LibvirtClusterIdentityCustomValidator) ValidateUpdate(
	_ context.Context,
	_, newObj *infrastructurev1alpha1.LibvirtClusterIdentity,
) (admission.Warnings, error) {
	libvirtclusteridentitylog.Info("Validation for LibvirtClusterIdentity upon update", "name", newObj.GetName())
	return validateIdentity(newObj)
}

// ValidateDelete implements webhook.CustomValidator.
func (v *LibvirtClusterIdentityCustomValidator) ValidateDelete(
	_ context.Context,
	_ *infrastructurev1alpha1.LibvirtClusterIdentity,
) (admission.Warnings, error) {
	return nil, nil
}

func validateIdentity(identity *infrastructurev1alpha1.LibvirtClusterIdentity) (admission.Warnings, error) {
	var allErrs field.ErrorList
	specPath := field.NewPath("spec")

	for _, msg := range validation.IsDNS1123Subdomain(identity.Spec.SecretName) {
		allErrs = append(allErrs, field.Invalid(specPath.Child("secretName"), identity.Spec.SecretName, msg))
	}

	var warnings admission.Warnings
	if allowed := identity.Spec.AllowedNamespaces; allowed == nil {
		warnings = append(warnings, "spec.allowedNamespaces is unset, so no LibvirtHost can use this identity; "+
			"set it to {} to allow every namespace")
	} else {
		allowedPath := specPath.Child("allowedNamespaces")
		for i, ns := range allowed.List {
			for _, msg := range validation.IsDNS1123Label(ns) {
				allErrs = append(allErrs, field.Invalid(allowedPath.Child("list").Index(i), ns, msg))
			}
		}
		if allowed.Selector != nil {
			// The controller converts the selector on every connection; an
			// invalid one would only surface as a failure in host status.
			allErrs = append(allErrs, metav1validation.ValidateLabelSelector(allowed.Selector,
				metav1validation.LabelSelectorValidationOptions{}, allowedPath.Child("selector"))...)
		}
	}

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: infrastructurev1alpha1.GroupVersion.Group, Kind: "LibvirtClusterIdentity"},
			identity.Name, allErrs)
	}
	return warnings, nil
}
