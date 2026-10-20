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
	"regexp"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	infrastructurev1alpha1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
	"github.com/on-prem-spot/cluster-api-provider-libvirt/internal/libvirt"
	libvirtssh "github.com/on-prem-spot/cluster-api-provider-libvirt/internal/ssh"
)

var libvirthostlog = logf.Log.WithName("libvirthost-resource")

// hostKeyFingerprintPattern matches the OpenSSH SHA256 fingerprint format
// produced by ssh.FingerprintSHA256 and `ssh-keygen -lf`: "SHA256:" followed
// by the unpadded base64 of a 32-byte digest.
var hostKeyFingerprintPattern = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{43}$`)

// SetupLibvirtHostWebhookWithManager registers the webhook for LibvirtHost in
// the manager. allowInsecureHostKeys mirrors the manager flag of the same
// name; it only decides whether a missing fingerprint draws a warning.
func SetupLibvirtHostWebhookWithManager(mgr ctrl.Manager, allowInsecureHostKeys bool) error {
	return ctrl.NewWebhookManagedBy(mgr, &infrastructurev1alpha1.LibvirtHost{}).
		WithValidator(&LibvirtHostCustomValidator{
			Client:                mgr.GetClient(),
			AllowInsecureHostKeys: allowInsecureHostKeys,
		}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-infrastructure-cluster-x-k8s-io-v1alpha1-libvirthost,mutating=false,failurePolicy=fail,sideEffects=None,groups=infrastructure.cluster.x-k8s.io,resources=libvirthosts,verbs=create;update,versions=v1alpha1,name=vlibvirthost-v1alpha1.kb.io,admissionReviewVersions=v1

// LibvirtHostCustomValidator validates LibvirtHost on create and update.
type LibvirtHostCustomValidator struct {
	// Client looks up machines placed on a host. Nil skips the checks that
	// need it.
	Client                client.Reader
	AllowInsecureHostKeys bool
}

// ValidateCreate implements webhook.CustomValidator.
func (v *LibvirtHostCustomValidator) ValidateCreate(
	ctx context.Context,
	obj *infrastructurev1alpha1.LibvirtHost,
) (admission.Warnings, error) {
	libvirthostlog.Info("Validation for LibvirtHost upon creation", "name", obj.GetName())

	if errs := validateHostSpec(obj); len(errs) > 0 {
		return nil, invalidHost(obj, errs)
	}
	return append(v.hostKeyWarnings(obj.Spec), v.identityWarnings(ctx, obj)...), nil
}

// ValidateUpdate implements webhook.CustomValidator.
//
// The URI names the hypervisor a host's VMs live on. Changing it while
// machines are placed on the host would point the controller at a different
// daemon -- or a different machine -- where those domains do not exist, so
// deletion would report success and leak the real VMs. With no machines on the
// host, the URI may change (e.g. the host was renumbered).
func (v *LibvirtHostCustomValidator) ValidateUpdate(
	ctx context.Context,
	oldObj, newObj *infrastructurev1alpha1.LibvirtHost,
) (admission.Warnings, error) {
	libvirthostlog.Info("Validation for LibvirtHost upon update", "name", newObj.GetName())

	allErrs := validateHostSpec(newObj)
	if oldObj.Spec.URI != newObj.Spec.URI && v.Client != nil {
		if err := v.validateNoMachinesPlaced(ctx, newObj); err != nil {
			allErrs = append(allErrs, err)
		}
	}
	if len(allErrs) > 0 {
		return nil, invalidHost(newObj, allErrs)
	}
	return append(v.hostKeyWarnings(newObj.Spec), v.identityWarnings(ctx, newObj)...), nil
}

// ValidateDelete implements webhook.CustomValidator.
func (v *LibvirtHostCustomValidator) ValidateDelete(
	_ context.Context,
	_ *infrastructurev1alpha1.LibvirtHost,
) (admission.Warnings, error) {
	return nil, nil
}

func validateHostSpec(host *infrastructurev1alpha1.LibvirtHost) field.ErrorList {
	var allErrs field.ErrorList
	spec := host.Spec
	specPath := field.NewPath("spec")

	// The controller reaches every host over SSH and runs virsh against the
	// daemon the URI path selects, so both halves are checked here rather
	// than on first connection.
	if _, _, port, err := libvirtssh.ParseLibvirtURI(spec.URI); err != nil {
		allErrs = append(allErrs, field.Invalid(specPath.Child("uri"), spec.URI,
			fmt.Sprintf("must be an SSH libvirt URI such as qemu+ssh://user@host/system: %v", err)))
	} else if port < 1 || port > 65535 {
		allErrs = append(allErrs, field.Invalid(specPath.Child("uri"), spec.URI, "port must be between 1 and 65535"))
	} else if _, err := libvirt.LocalConnectionURI(spec.URI); err != nil {
		allErrs = append(allErrs, field.Invalid(specPath.Child("uri"), spec.URI, err.Error()))
	}

	switch {
	case spec.SecretRef != nil && spec.IdentityRef != nil:
		allErrs = append(allErrs, field.Forbidden(specPath.Child("identityRef"),
			"set either secretRef or identityRef, not both"))
	case spec.IdentityRef != nil:
		if spec.IdentityRef.Name == "" {
			allErrs = append(allErrs, field.Required(specPath.Child("identityRef", "name"),
				"the name of a LibvirtClusterIdentity is required"))
		}
	case spec.SecretRef == nil || spec.SecretRef.Name == "":
		allErrs = append(allErrs, field.Required(specPath.Child("secretRef", "name"),
			"a Secret holding the SSH private key, or an identityRef, is required"))
	case spec.SecretRef.Namespace != "" && spec.SecretRef.Namespace != host.Namespace:
		// The controller refuses this at reconcile time too; rejecting it here
		// reports the mistake to the author instead of in host status.
		allErrs = append(allErrs, field.Forbidden(specPath.Child("secretRef", "namespace"),
			fmt.Sprintf("the Secret must be in the host's namespace %q", host.Namespace)))
	}

	if fp := spec.HostKeyFingerprint; fp != "" && !hostKeyFingerprintPattern.MatchString(fp) {
		allErrs = append(allErrs, field.Invalid(specPath.Child("hostKeyFingerprint"), fp,
			"must be an OpenSSH SHA256 fingerprint (SHA256: followed by 43 base64 characters), "+
				"as printed by `ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub`"))
	}

	// Both paths are handed to libvirt on the host, which resolves them
	// against no working directory: a relative path fails at domain start.
	for _, p := range []struct {
		name, value string
	}{
		{"firmwarePath", spec.FirmwarePath},
		{"nvramTemplatePath", spec.NVRAMTemplatePath},
	} {
		if p.value != "" && !strings.HasPrefix(p.value, "/") {
			allErrs = append(allErrs, field.Invalid(specPath.Child(p.name), p.value, "must be an absolute path"))
		}
	}
	return allErrs
}

// validateNoMachinesPlaced forbids a URI change while any LibvirtMachine still
// references the host, including machines being deleted: their cleanup still
// needs the original hypervisor.
func (v *LibvirtHostCustomValidator) validateNoMachinesPlaced(
	ctx context.Context,
	host *infrastructurev1alpha1.LibvirtHost,
) *field.Error {
	path := field.NewPath("spec", "uri")
	machines := &infrastructurev1alpha1.LibvirtMachineList{}
	if err := v.Client.List(ctx, machines, client.InNamespace(host.Namespace)); err != nil {
		return field.InternalError(path, fmt.Errorf("failed to list machines: %w", err))
	}
	for i := range machines.Items {
		if machines.Items[i].Spec.HostRef.Name == host.Name {
			return field.Forbidden(path, fmt.Sprintf(
				"uri cannot change while LibvirtMachine %q is placed on this host; "+
					"delete the machines on it first", machines.Items[i].Name))
		}
	}
	return nil
}

func (v *LibvirtHostCustomValidator) hostKeyWarnings(spec infrastructurev1alpha1.LibvirtHostSpec) admission.Warnings {
	if spec.HostKeyFingerprint != "" {
		return nil
	}
	if v.AllowInsecureHostKeys {
		return admission.Warnings{"spec.hostKeyFingerprint is unset; the controller will accept any SSH host key " +
			"for this host because the manager runs with --allow-insecure-host-keys"}
	}
	return admission.Warnings{"spec.hostKeyFingerprint is unset; the controller will refuse to connect to this " +
		"host unless the manager runs with --allow-insecure-host-keys"}
}

// identityWarnings warns when spec.identityRef names an identity that is
// missing or does not allow the host's namespace. These are warnings, not
// errors: the identity may be created or widened later, and the controller
// checks again on every connection.
func (v *LibvirtHostCustomValidator) identityWarnings(
	ctx context.Context,
	host *infrastructurev1alpha1.LibvirtHost,
) admission.Warnings {
	if v.Client == nil || host.Spec.IdentityRef == nil || host.Spec.IdentityRef.Name == "" {
		return nil
	}
	name := host.Spec.IdentityRef.Name
	identity := &infrastructurev1alpha1.LibvirtClusterIdentity{}
	if err := v.Client.Get(ctx, client.ObjectKey{Name: name}, identity); err != nil {
		if apierrors.IsNotFound(err) {
			return admission.Warnings{fmt.Sprintf("LibvirtClusterIdentity %q does not exist yet; "+
				"the controller cannot connect to this host until it does", name)}
		}
		return nil
	}
	var nsLabels map[string]string
	if identity.NeedsNamespaceLabels() {
		ns := &corev1.Namespace{}
		if err := v.Client.Get(ctx, client.ObjectKey{Name: host.Namespace}, ns); err != nil {
			return nil
		}
		nsLabels = ns.Labels
	}
	allowed, err := identity.AllowsNamespace(host.Namespace, nsLabels)
	if err != nil {
		return admission.Warnings{err.Error()}
	}
	if !allowed {
		return admission.Warnings{fmt.Sprintf("LibvirtClusterIdentity %q does not allow namespace %q in "+
			"spec.allowedNamespaces; the controller will refuse to use it for this host", name, host.Namespace)}
	}
	return nil
}

func invalidHost(obj *infrastructurev1alpha1.LibvirtHost, allErrs field.ErrorList) error {
	return apierrors.NewInvalid(
		schema.GroupKind{
			Group: infrastructurev1alpha1.GroupVersion.Group,
			Kind:  "LibvirtHost",
		},
		obj.Name,
		allErrs,
	)
}
