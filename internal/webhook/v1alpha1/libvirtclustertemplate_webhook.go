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
	"fmt"
	"reflect"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	infrastructurev1alpha1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

var libvirtclustertemplatelog = logf.Log.WithName("libvirtclustertemplate-resource")

// SetupLibvirtClusterTemplateWebhookWithManager registers the webhook for
// LibvirtClusterTemplate in the manager.
func SetupLibvirtClusterTemplateWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &infrastructurev1alpha1.LibvirtClusterTemplate{}).
		WithValidator(&LibvirtClusterTemplateCustomValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-infrastructure-cluster-x-k8s-io-v1alpha1-libvirtclustertemplate,mutating=false,failurePolicy=fail,sideEffects=None,groups=infrastructure.cluster.x-k8s.io,resources=libvirtclustertemplates,verbs=create;update,versions=v1alpha1,name=vlibvirtclustertemplate-v1alpha1.kb.io,admissionReviewVersions=v1

// LibvirtClusterTemplateCustomValidator validates LibvirtClusterTemplate on
// create and update.
type LibvirtClusterTemplateCustomValidator struct{}

// ValidateCreate implements webhook.CustomValidator.
func (v *LibvirtClusterTemplateCustomValidator) ValidateCreate(
	_ context.Context,
	obj *infrastructurev1alpha1.LibvirtClusterTemplate,
) (admission.Warnings, error) {
	libvirtclustertemplatelog.Info("Validation for LibvirtClusterTemplate upon creation", "name", obj.GetName())

	if errs := validateTemplatedClusterSpec(obj.Spec.Template.Spec); len(errs) > 0 {
		return nil, invalidClusterTemplate(obj, errs)
	}
	return endpointWarnings(obj), nil
}

// ValidateUpdate implements webhook.CustomValidator.
//
// The templated spec is immutable, for the same reason as
// LibvirtMachineTemplate: clusters already stamped from a template are not
// restamped, so editing one in place leaves running clusters that silently no
// longer match it. ClusterClass changes machine and cluster configuration by
// referencing a new template, not by mutating the old one.
func (v *LibvirtClusterTemplateCustomValidator) ValidateUpdate(
	_ context.Context,
	oldObj, newObj *infrastructurev1alpha1.LibvirtClusterTemplate,
) (admission.Warnings, error) {
	libvirtclustertemplatelog.Info("Validation for LibvirtClusterTemplate upon update", "name", newObj.GetName())

	var allErrs field.ErrorList
	if !reflect.DeepEqual(oldObj.Spec.Template.Spec, newObj.Spec.Template.Spec) {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec", "template", "spec"),
			"<redacted>",
			"LibvirtClusterTemplate spec.template.spec is immutable; "+
				"create a new template and point the ClusterClass at it",
		))
	}
	allErrs = append(allErrs, validateTemplatedClusterSpec(newObj.Spec.Template.Spec)...)

	if len(allErrs) > 0 {
		return nil, invalidClusterTemplate(newObj, allErrs)
	}
	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator.
func (v *LibvirtClusterTemplateCustomValidator) ValidateDelete(
	_ context.Context,
	_ *infrastructurev1alpha1.LibvirtClusterTemplate,
) (admission.Warnings, error) {
	return nil, nil
}

// validateTemplatedClusterSpec checks the parts of a cluster spec that are
// wrong to template regardless of the cluster they land on.
func validateTemplatedClusterSpec(spec infrastructurev1alpha1.LibvirtClusterSpec) field.ErrorList {
	var allErrs field.ErrorList
	base := field.NewPath("spec", "template", "spec")

	// A port is either absent or usable; a template carrying an unusable one
	// would stamp it onto every cluster and fail each of them identically.
	if port := spec.ControlPlaneEndpoint.Port; port < 0 || port > 65535 {
		allErrs = append(allErrs, field.Invalid(
			base.Child("controlPlaneEndpoint", "port"), port,
			"port must be between 0 and 65535",
		))
	}
	if spec.ControlPlaneEndpoint.Host == "" && spec.ControlPlaneEndpoint.Port != 0 {
		allErrs = append(allErrs, field.Invalid(
			base.Child("controlPlaneEndpoint"), spec.ControlPlaneEndpoint,
			"port is set without a host; set both or neither",
		))
	}
	return allErrs
}

// endpointWarnings reports, without blocking, that a templated control plane
// endpoint is copied onto every cluster created from the template.
//
// This cannot be an error: a single-cluster topology with a known endpoint is
// a legitimate use, and the field is part of the embedded spec.
func endpointWarnings(obj *infrastructurev1alpha1.LibvirtClusterTemplate) admission.Warnings {
	endpoint := obj.Spec.Template.Spec.ControlPlaneEndpoint
	if endpoint.Host == "" {
		return nil
	}
	return admission.Warnings{fmt.Sprintf(
		"spec.template.spec.controlPlaneEndpoint is set to %s:%d and is copied onto every "+
			"LibvirtCluster created from this template, so more than one cluster would share "+
			"one API endpoint. Leave it unset unless the topology creates a single cluster; a "+
			"cluster without an endpoint waits with reason %s until one is set.",
		endpoint.Host, endpoint.Port, infrastructurev1alpha1.ReasonWaitingForControlPlaneEndpoint,
	)}
}

func invalidClusterTemplate(
	obj *infrastructurev1alpha1.LibvirtClusterTemplate,
	allErrs field.ErrorList,
) error {
	return apierrors.NewInvalid(
		schema.GroupKind{
			Group: infrastructurev1alpha1.GroupVersion.Group,
			Kind:  "LibvirtClusterTemplate",
		},
		obj.Name,
		allErrs,
	)
}
