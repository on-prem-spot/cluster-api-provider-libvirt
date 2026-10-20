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
	"maps"
	"net"
	"reflect"
	"regexp"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1validation "k8s.io/apimachinery/pkg/apis/meta/v1/validation"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	infrastructurev1alpha1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

// nolint:unused
// log is for logging in this package.
var libvirtmachinelog = logf.Log.WithName("libvirtmachine-resource")

var libvirtIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)

// SetupLibvirtMachineWebhookWithManager registers the webhook for LibvirtMachine in the manager.
func SetupLibvirtMachineWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &infrastructurev1alpha1.LibvirtMachine{}).
		WithValidator(&LibvirtMachineCustomValidator{Client: mgr.GetClient()}).
		WithDefaulter(&LibvirtMachineCustomDefaulter{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-infrastructure-cluster-x-k8s-io-v1alpha1-libvirtmachine,mutating=false,failurePolicy=fail,sideEffects=None,groups=infrastructure.cluster.x-k8s.io,resources=libvirtmachines,verbs=create;update,versions=v1alpha1,name=vlibvirtmachine-v1alpha1.kb.io,admissionReviewVersions=v1

// LibvirtMachineCustomValidator struct is responsible for validating the LibvirtMachine resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type LibvirtMachineCustomValidator struct {
	Client client.Reader
}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type LibvirtMachine.
func (v *LibvirtMachineCustomValidator) ValidateCreate(ctx context.Context, obj *infrastructurev1alpha1.LibvirtMachine) (admission.Warnings, error) {
	libvirtmachinelog.Info("Validation for LibvirtMachine upon creation", "name", obj.GetName())

	var allErrs field.ErrorList

	allErrs = append(allErrs, validatePlacement(obj.Spec)...)

	// Addresses may be left to the host's spec.machineNetwork; whether the
	// two together give one is checked once the host is known.
	allErrs = append(allErrs, validateCIDRs(field.NewPath("spec", "network", "addresses"), obj.Spec.Network.Addresses)...)

	allErrs = append(allErrs, validateShellSafeLibvirtFields(obj.Spec)...)
	allErrs = append(allErrs, validateNodeMetadata(obj.Spec, nil)...)

	// Reject full-clone when baseImagePool differs from storagePool.
	// virsh vol-clone cannot clone across pools.
	if obj.Spec.RootDisk.CloneStrategy == infrastructurev1alpha1.CloneStrategyFullClone {
		baseImagePool := obj.Spec.RootDisk.BaseImagePool
		if baseImagePool == "" {
			baseImagePool = obj.Spec.RootDisk.StoragePool
		}
		if baseImagePool != obj.Spec.RootDisk.StoragePool {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec", "rootDisk", "cloneStrategy"),
				string(obj.Spec.RootDisk.CloneStrategy),
				"full-clone is not supported when baseImagePool differs from storagePool; use copy-on-write instead",
			))
		}
	}

	// Enforce one machine per pinned host; a selector claims a free one.
	if v.Client != nil && obj.Spec.HostRef != nil {
		if err := v.validateHostUniqueness(ctx, obj); err != nil {
			allErrs = append(allErrs, err)
		}
	}

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: infrastructurev1alpha1.GroupVersion.Group, Kind: "LibvirtMachine"},
			obj.Name,
			allErrs,
		)
	}

	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type LibvirtMachine.
func (v *LibvirtMachineCustomValidator) ValidateUpdate(_ context.Context, oldObj, newObj *infrastructurev1alpha1.LibvirtMachine) (admission.Warnings, error) {
	libvirtmachinelog.Info("Validation for LibvirtMachine upon update", "name", newObj.GetName())

	var allErrs field.ErrorList

	allErrs = append(allErrs, validateShellSafeLibvirtFields(newObj.Spec)...)
	allErrs = append(allErrs, validateNodeMetadata(newObj.Spec, &oldObj.Spec)...)

	// ProviderID transitions: only nil->value is allowed. Once set, it cannot
	// be changed or cleared.
	if oldObj.Spec.ProviderID != nil {
		if newObj.Spec.ProviderID == nil {
			allErrs = append(allErrs, field.Forbidden(
				field.NewPath("spec", "providerID"),
				"providerID cannot be cleared once set",
			))
		} else if *oldObj.Spec.ProviderID != *newObj.Spec.ProviderID {
			allErrs = append(allErrs, field.Forbidden(
				field.NewPath("spec", "providerID"),
				"providerID cannot be changed once set",
			))
		}
	}

	// Compare specs excluding providerID.
	oldSpec := oldObj.Spec.DeepCopy()
	newSpec := newObj.Spec.DeepCopy()
	oldSpec.ProviderID = nil
	newSpec.ProviderID = nil

	if !reflect.DeepEqual(oldSpec, newSpec) {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec"),
			"spec is immutable after creation (except providerID)",
		))
	}

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: infrastructurev1alpha1.GroupVersion.Group, Kind: "LibvirtMachine"},
			newObj.Name,
			allErrs,
		)
	}

	return nil, nil
}

// reservedNodeKeyDomains are label/annotation domains a LibvirtMachine author
// may not write to a Node. The controller patches Nodes with its own
// cluster-scoped identity, so without this an author who holds only namespaced
// LibvirtMachine rights could set keys that decide scheduling, policy, and node
// role — which is exactly the escalation the NodeRestriction admission plugin
// exists to prevent for kubelets. Everything outside these domains stays
// writable, so third-party keys such as "dynatrace", "aqua" or
// "k8s.ovn.org/egress-assignable" are unaffected.
var reservedNodeKeyDomains = []string{
	"node-restriction.kubernetes.io",
	"node-role.kubernetes.io",
	"kubernetes.io",
	"k8s.io",
}

// reservedNodeAnnotations are the annotations CAPLV uses to track which keys it
// owns on a Node. Letting an author set them directly would let them name any
// existing label as CAPLV-managed and have the next reconcile delete it.
var reservedNodeAnnotations = []string{
	infrastructurev1alpha1.ManagedNodeLabelsAnnotation,
	infrastructurev1alpha1.ManagedNodeAnnotationsAnnotation,
}

// keyDomain returns the domain part of a label/annotation key ("" when the key
// is unprefixed, e.g. "beta.kubernetes.io/arch" -> "beta.kubernetes.io").
func keyDomain(key string) string {
	domain, _, ok := strings.Cut(key, "/")
	if !ok {
		return ""
	}
	return strings.ToLower(domain)
}

// isReservedNodeKey reports whether key sits in a reserved domain, matching the
// domain itself and any subdomain of it.
func isReservedNodeKey(key string) bool {
	domain := keyDomain(key)
	if domain == "" {
		return false
	}
	for _, reserved := range reservedNodeKeyDomains {
		if domain == reserved || strings.HasSuffix(domain, "."+reserved) {
			return true
		}
	}
	return false
}

// validateNodeMetadata rejects reserved keys in spec.nodeLabels and
// spec.nodeAnnotations. A nil oldSpec (create) checks every key; on update only
// keys this change introduces or alters are checked.
//
// Carrying a pre-existing key forward has to stay legal. ValidateUpdate already
// makes both fields immutable, so on update the only reserved keys reachable
// here are ones written before this restriction existed — and the reconciler
// sets spec.providerID, which is itself an update, so rejecting them would wedge
// the machine short of Ready rather than protect anything. Checking only the
// delta keeps the guard meaningful if that immutability rule is ever relaxed.
func validateNodeMetadata(
	spec infrastructurev1alpha1.LibvirtMachineSpec,
	oldSpec *infrastructurev1alpha1.LibvirtMachineSpec,
) field.ErrorList {
	var allErrs field.ErrorList

	var oldLabels, oldAnnotations map[string]string
	if oldSpec != nil {
		oldLabels, oldAnnotations = oldSpec.NodeLabels, oldSpec.NodeAnnotations
	}

	labelsPath := field.NewPath("spec", "nodeLabels")
	for _, k := range slices.Sorted(maps.Keys(spec.NodeLabels)) {
		if carriedForward(oldLabels, k, spec.NodeLabels[k]) {
			continue
		}
		if isReservedNodeKey(k) {
			allErrs = append(allErrs, field.Forbidden(labelsPath.Key(k),
				fmt.Sprintf("key is in the reserved domain %q and may not be set on a Node by a LibvirtMachine", keyDomain(k))))
		}
	}

	annotationsPath := field.NewPath("spec", "nodeAnnotations")
	for _, k := range slices.Sorted(maps.Keys(spec.NodeAnnotations)) {
		if carriedForward(oldAnnotations, k, spec.NodeAnnotations[k]) {
			continue
		}
		if slices.Contains(reservedNodeAnnotations, k) {
			allErrs = append(allErrs, field.Forbidden(annotationsPath.Key(k),
				"annotation is reserved for CAPLV's own node-ownership bookkeeping"))
			continue
		}
		if isReservedNodeKey(k) {
			allErrs = append(allErrs, field.Forbidden(annotationsPath.Key(k),
				fmt.Sprintf("key is in the reserved domain %q and may not be set on a Node by a LibvirtMachine", keyDomain(k))))
		}
	}

	return allErrs
}

// carriedForward reports whether key already held value before this update.
func carriedForward(old map[string]string, key, value string) bool {
	prev, ok := old[key]
	return ok && prev == value
}

func validateShellSafeLibvirtFields(spec infrastructurev1alpha1.LibvirtMachineSpec) field.ErrorList {
	var allErrs field.ErrorList

	rootPath := field.NewPath("spec", "rootDisk")
	allErrs = appendLibvirtIdentifierError(allErrs, rootPath.Child("storagePool"), spec.RootDisk.StoragePool)
	allErrs = appendLibvirtIdentifierError(allErrs, rootPath.Child("baseImage"), spec.RootDisk.BaseImage)
	if spec.RootDisk.BaseImagePool != "" {
		allErrs = appendLibvirtIdentifierError(allErrs, rootPath.Child("baseImagePool"), spec.RootDisk.BaseImagePool)
	}

	disksPath := field.NewPath("spec", "additionalDisks")
	for i, disk := range spec.AdditionalDisks {
		diskPath := disksPath.Index(i)
		allErrs = appendLibvirtIdentifierError(allErrs, diskPath.Child("name"), disk.Name)
		allErrs = appendLibvirtIdentifierError(allErrs, diskPath.Child("storagePool"), disk.StoragePool)
	}

	return allErrs
}

func appendLibvirtIdentifierError(allErrs field.ErrorList, path *field.Path, value string) field.ErrorList {
	if value == "" || libvirtIdentifierPattern.MatchString(value) {
		return allErrs
	}
	return append(allErrs, field.Invalid(path, value, "must contain only letters, numbers, '_', '.', ':', or '-'"))
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type LibvirtMachine.
func (v *LibvirtMachineCustomValidator) ValidateDelete(_ context.Context, _ *infrastructurev1alpha1.LibvirtMachine) (admission.Warnings, error) {
	return nil, nil
}

// validatePlacement requires exactly one of hostRef and hostSelector, and a
// well-formed selector. The CRD enforces the first too; checking it here
// gives a clearer message.
func validatePlacement(spec infrastructurev1alpha1.LibvirtMachineSpec) field.ErrorList {
	var allErrs field.ErrorList
	specPath := field.NewPath("spec")
	switch {
	case spec.HostRef != nil && spec.HostSelector != nil:
		allErrs = append(allErrs, field.Forbidden(specPath.Child("hostSelector"),
			"set either hostRef or hostSelector, not both"))
	case spec.HostRef == nil && spec.HostSelector == nil:
		allErrs = append(allErrs, field.Required(specPath.Child("hostRef"),
			"a hostRef naming a LibvirtHost, or a hostSelector, is required"))
	case spec.HostSelector != nil:
		allErrs = append(allErrs, metav1validation.ValidateLabelSelector(spec.HostSelector,
			metav1validation.LabelSelectorValidationOptions{}, specPath.Child("hostSelector"))...)
	}
	return allErrs
}

// validateCIDRs checks that each address is in CIDR notation.
func validateCIDRs(path *field.Path, addresses []string) field.ErrorList {
	var allErrs field.ErrorList
	for i, addr := range addresses {
		if _, _, err := net.ParseCIDR(addr); err != nil {
			allErrs = append(allErrs, field.Invalid(path.Index(i), addr, fmt.Sprintf("invalid CIDR notation: %v", err)))
		}
	}
	return allErrs
}

// validateHostUniqueness rejects a machine pinned to a host another
// LibvirtMachine in the namespace already uses, whether by its own hostRef or
// by claim. Each host runs at most one CAPLV-managed VM; the controller's
// claim enforces it, and this reports the mistake at admission.
func (v *LibvirtMachineCustomValidator) validateHostUniqueness(ctx context.Context, obj *infrastructurev1alpha1.LibvirtMachine) *field.Error {
	hostName := obj.Spec.HostRef.Name
	host := &infrastructurev1alpha1.LibvirtHost{}
	err := v.Client.Get(ctx, client.ObjectKey{Namespace: obj.Namespace, Name: hostName}, host)
	switch {
	case err == nil && host.Spec.ConsumerRef != nil && host.Spec.ConsumerRef.Name != obj.Name:
		return field.Forbidden(field.NewPath("spec", "hostRef"),
			fmt.Sprintf("host %q is already in use by LibvirtMachine %q; each host supports at most one VM",
				hostName, host.Spec.ConsumerRef.Name))
	case err != nil && !apierrors.IsNotFound(err):
		return field.InternalError(field.NewPath("spec", "hostRef"), fmt.Errorf("failed to get LibvirtHost: %w", err))
	}

	machineList := &infrastructurev1alpha1.LibvirtMachineList{}
	if err := v.Client.List(ctx, machineList, client.InNamespace(obj.Namespace)); err != nil {
		return field.InternalError(
			field.NewPath("spec", "hostRef"),
			fmt.Errorf("failed to list machines: %w", err),
		)
	}
	for i := range machineList.Items {
		existing := &machineList.Items[i]
		// Skip self (in case of re-validation).
		if existing.Name == obj.Name && existing.Namespace == obj.Namespace {
			continue
		}
		// Skip machines that are being deleted.
		if !existing.DeletionTimestamp.IsZero() {
			continue
		}
		if existing.AssignedHostName() == hostName {
			return field.Forbidden(
				field.NewPath("spec", "hostRef"),
				fmt.Sprintf("host %q is already in use by LibvirtMachine %q; each host supports at most one VM",
					hostName, existing.Name),
			)
		}
	}
	return nil
}
