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
	"reflect"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	infrastructurev1alpha1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

var libvirtmachinetemplatelog = logf.Log.WithName("libvirtmachinetemplate-resource")

// SetupLibvirtMachineTemplateWebhookWithManager registers the webhook for
// LibvirtMachineTemplate in the manager.
func SetupLibvirtMachineTemplateWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &infrastructurev1alpha1.LibvirtMachineTemplate{}).
		WithValidator(&LibvirtMachineTemplateCustomValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-infrastructure-cluster-x-k8s-io-v1alpha1-libvirtmachinetemplate,mutating=false,failurePolicy=fail,sideEffects=None,groups=infrastructure.cluster.x-k8s.io,resources=libvirtmachinetemplates,verbs=create;update,versions=v1alpha1,name=vlibvirtmachinetemplate-v1alpha1.kb.io,admissionReviewVersions=v1

// LibvirtMachineTemplateCustomValidator validates LibvirtMachineTemplate on
// create and update.
//
// It deliberately holds no client. The machine webhook reads the API to enforce
// one machine per host, but a template is not a machine: it reserves no host,
// so there is nothing to check for uniqueness until machines are stamped from
// it, at which point the machine webhook does the checking.
type LibvirtMachineTemplateCustomValidator struct{}

// ValidateCreate implements webhook.CustomValidator.
func (v *LibvirtMachineTemplateCustomValidator) ValidateCreate(
	_ context.Context,
	obj *infrastructurev1alpha1.LibvirtMachineTemplate,
) (admission.Warnings, error) {
	libvirtmachinetemplatelog.Info("Validation for LibvirtMachineTemplate upon creation", "name", obj.GetName())

	if errs := validateTemplatedMachineSpec(obj.Spec.Template.Spec, nil); len(errs) > 0 {
		return nil, invalidTemplate(obj, errs)
	}
	return replicaScalingWarnings(obj), nil
}

// ValidateUpdate implements webhook.CustomValidator.
//
// The templated spec is immutable, as it is for the other providers' machine
// templates. A MachineDeployment that has already stamped machines from a
// template would not restamp them, so editing one in place yields running
// machines that silently no longer match it; the CAPI way to change machine
// configuration is to create a new template and roll onto it.
func (v *LibvirtMachineTemplateCustomValidator) ValidateUpdate(
	_ context.Context,
	oldObj, newObj *infrastructurev1alpha1.LibvirtMachineTemplate,
) (admission.Warnings, error) {
	libvirtmachinetemplatelog.Info("Validation for LibvirtMachineTemplate upon update", "name", newObj.GetName())

	var allErrs field.ErrorList
	if !reflect.DeepEqual(oldObj.Spec.Template.Spec, newObj.Spec.Template.Spec) {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec", "template", "spec"),
			"<redacted>",
			"LibvirtMachineTemplate spec.template.spec is immutable; "+
				"create a new template and update the MachineDeployment or control plane to reference it",
		))
	}
	// Still validate content, so a template created before a rule existed
	// reports why it is now invalid rather than only that it cannot change.
	allErrs = append(allErrs, validateTemplatedMachineSpec(newObj.Spec.Template.Spec, &oldObj.Spec.Template.Spec)...)

	if len(allErrs) > 0 {
		return nil, invalidTemplate(newObj, allErrs)
	}
	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator.
func (v *LibvirtMachineTemplateCustomValidator) ValidateDelete(
	_ context.Context,
	_ *infrastructurev1alpha1.LibvirtMachineTemplate,
) (admission.Warnings, error) {
	return nil, nil
}

// validateTemplatedMachineSpec applies the LibvirtMachine spec rules to the
// spec a template stamps out, so a template cannot be used to create machines
// that the machine webhook would have rejected.
func validateTemplatedMachineSpec(
	spec infrastructurev1alpha1.LibvirtMachineSpec,
	oldSpec *infrastructurev1alpha1.LibvirtMachineSpec,
) field.ErrorList {
	var allErrs field.ErrorList
	allErrs = append(allErrs, validateShellSafeLibvirtFields(spec)...)
	allErrs = append(allErrs, validateNodeMetadata(spec, oldSpec)...)
	return rerootFieldErrors(allErrs)
}

// rerootFieldErrors rewrites a "spec.…" field path to "spec.template.spec.…".
//
// The reused validators were written for a LibvirtMachine, where the spec is at
// the root, so their paths would otherwise point somewhere that does not exist
// on a template and send the reader to the wrong field.
func rerootFieldErrors(errs field.ErrorList) field.ErrorList {
	for _, e := range errs {
		if e.Field == "spec" {
			e.Field = "spec.template.spec"
			continue
		}
		if rest, ok := strings.CutPrefix(e.Field, "spec."); ok {
			e.Field = "spec.template.spec." + rest
		}
	}
	return errs
}

// replicaScalingWarnings reports, without blocking, that this template cannot
// back more than one replica.
//
// A LibvirtMachine names its host through the required spec.hostRef and
// declares a static network identity, and a host runs exactly one VM, so every
// machine stamped from one template asks for the same host and the same
// address. Admission cannot see the replica count, so this cannot be an error:
// a single-replica control plane is a legitimate use. It is a warning so the
// collision is visible when the template is written rather than when the second
// machine fails.
func replicaScalingWarnings(obj *infrastructurev1alpha1.LibvirtMachineTemplate) admission.Warnings {
	var warnings admission.Warnings
	if obj.Spec.Template.Spec.HostRef.Name != "" {
		warnings = append(warnings,
			"spec.template.spec.hostRef pins every machine from this template to host "+
				obj.Spec.Template.Spec.HostRef.Name+
				", and a host runs one VM; more than one replica will collide. "+
				"CAPLV does not allocate hosts per machine yet.")
	}
	if len(obj.Spec.Template.Spec.Network.Addresses) > 0 {
		warnings = append(warnings,
			"spec.template.spec.network.addresses assigns a static address to every machine from this "+
				"template; more than one replica will produce duplicate addresses. "+
				"CAPLV does not allocate addresses per machine yet.")
	}
	return warnings
}

func invalidTemplate(
	obj *infrastructurev1alpha1.LibvirtMachineTemplate,
	allErrs field.ErrorList,
) error {
	return apierrors.NewInvalid(
		schema.GroupKind{
			Group: infrastructurev1alpha1.GroupVersion.Group,
			Kind:  "LibvirtMachineTemplate",
		},
		obj.Name,
		allErrs,
	)
}
