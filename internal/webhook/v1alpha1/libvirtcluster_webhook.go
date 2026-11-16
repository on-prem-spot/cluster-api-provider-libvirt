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
	"net/url"
	posixpath "path"
	"regexp"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1validation "k8s.io/apimachinery/pkg/apis/meta/v1/validation"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	infrastructurev1alpha1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

// bootArtifactsHostDir is the directory on each libvirt host under which
// bootArtifacts.hostPath must lie: caplv-hostctl's persistent directory.
const bootArtifactsHostDir = "/var/lib/caplv"

var libvirtclusterlog = logf.Log.WithName("libvirtcluster-resource")

// sha256HexPattern matches a bare sha256 hex digest. The resolvers compare
// digests case-insensitively, so either case is accepted.
var sha256HexPattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

// SetupLibvirtClusterWebhookWithManager registers the webhook for
// LibvirtCluster in the manager. allowInsecureImageTransport mirrors the
// manager flag of the same name; it only decides whether insecure image
// sources draw a warning, since the controller enforces the policy itself.
func SetupLibvirtClusterWebhookWithManager(mgr ctrl.Manager, allowInsecureImageTransport bool) error {
	return ctrl.NewWebhookManagedBy(mgr, &infrastructurev1alpha1.LibvirtCluster{}).
		WithValidator(&LibvirtClusterCustomValidator{AllowInsecureImageTransport: allowInsecureImageTransport}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-infrastructure-cluster-x-k8s-io-v1alpha1-libvirtcluster,mutating=false,failurePolicy=fail,sideEffects=None,groups=infrastructure.cluster.x-k8s.io,resources=libvirtclusters,verbs=create;update,versions=v1alpha1,name=vlibvirtcluster-v1alpha1.kb.io,admissionReviewVersions=v1

// LibvirtClusterCustomValidator validates LibvirtCluster on create and update.
type LibvirtClusterCustomValidator struct {
	AllowInsecureImageTransport bool
}

// ValidateCreate implements webhook.CustomValidator.
func (v *LibvirtClusterCustomValidator) ValidateCreate(
	_ context.Context,
	obj *infrastructurev1alpha1.LibvirtCluster,
) (admission.Warnings, error) {
	libvirtclusterlog.Info("Validation for LibvirtCluster upon creation", "name", obj.GetName())

	if errs := validateClusterSpec(obj.Spec, field.NewPath("spec")); len(errs) > 0 {
		return nil, invalidCluster(obj, errs)
	}
	return v.insecureTransportWarnings(obj.Spec), nil
}

// ValidateUpdate implements webhook.CustomValidator.
//
// controlPlaneEndpoint is immutable once both host and port are set. CAPI
// copies it onto Cluster.spec.controlPlaneEndpoint and never re-reads it, and
// every kubeconfig and certificate already issued names the old endpoint, so a
// later edit would leave the cluster pointing somewhere the infrastructure no
// longer claims. bootArtifacts and baseImage stay mutable: they only affect
// machines provisioned after the change.
func (v *LibvirtClusterCustomValidator) ValidateUpdate(
	_ context.Context,
	oldObj, newObj *infrastructurev1alpha1.LibvirtCluster,
) (admission.Warnings, error) {
	libvirtclusterlog.Info("Validation for LibvirtCluster upon update", "name", newObj.GetName())

	allErrs := validateClusterSpec(newObj.Spec, field.NewPath("spec"))
	oldEndpoint := oldObj.Spec.ControlPlaneEndpoint
	if oldEndpoint.Host != "" && oldEndpoint.Port != 0 && newObj.Spec.ControlPlaneEndpoint != oldEndpoint {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec", "controlPlaneEndpoint"),
			fmt.Sprintf("controlPlaneEndpoint is immutable once set (currently %s:%d)", oldEndpoint.Host, oldEndpoint.Port),
		))
	}
	if len(allErrs) > 0 {
		return nil, invalidCluster(newObj, allErrs)
	}
	return v.insecureTransportWarnings(newObj.Spec), nil
}

// ValidateDelete implements webhook.CustomValidator.
func (v *LibvirtClusterCustomValidator) ValidateDelete(
	_ context.Context,
	_ *infrastructurev1alpha1.LibvirtCluster,
) (admission.Warnings, error) {
	return nil, nil
}

// validateClusterSpec checks a LibvirtCluster spec rooted at base, which is
// "spec" for a LibvirtCluster and "spec.template.spec" for a template.
func validateClusterSpec(spec infrastructurev1alpha1.LibvirtClusterSpec, base *field.Path) field.ErrorList {
	var allErrs field.ErrorList

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

	if ba := spec.BootArtifacts; ba != nil {
		allErrs = append(allErrs, validateBootArtifactsSource(ba.Source, base.Child("bootArtifacts", "source"))...)
		// caplv-hostctl writes host files only under /run/caplv, a tmpfs, and
		// /var/lib/caplv; a VM needs its kernel at every boot, so only the
		// persistent directory will do.
		if hostPath := posixpath.Clean(ba.HostPath); hostPath != bootArtifactsHostDir &&
			!strings.HasPrefix(hostPath, bootArtifactsHostDir+"/") {
			allErrs = append(allErrs, field.Invalid(base.Child("bootArtifacts", "hostPath"), ba.HostPath,
				"must be "+bootArtifactsHostDir+" or a directory under it, where the host helper may write "+
					"and which survives host reboots"))
		}
	}
	if bi := spec.BaseImage; bi != nil {
		allErrs = append(allErrs, validateBaseImageSource(bi.Source, base.Child("baseImage", "source"))...)
	}
	if sel := spec.ControlPlaneHostSelector; sel != nil {
		allErrs = append(allErrs, metav1validation.ValidateLabelSelector(sel,
			metav1validation.LabelSelectorValidationOptions{}, base.Child("controlPlaneHostSelector"))...)
	}
	return allErrs
}

// validateSourceMembers requires the member matching type to be set and every
// other member to be unset, so the object says exactly where bytes come from.
func validateSourceMembers(
	path *field.Path,
	sourceType infrastructurev1alpha1.BootArtifactsSourceType,
	httpsSet, ociSet, s3Set bool,
) field.ErrorList {
	var allErrs field.ErrorList
	members := []struct {
		name string
		typ  infrastructurev1alpha1.BootArtifactsSourceType
		set  bool
	}{
		{"https", infrastructurev1alpha1.BootArtifactsSourceHTTPS, httpsSet},
		{"oci", infrastructurev1alpha1.BootArtifactsSourceOCI, ociSet},
		{"s3", infrastructurev1alpha1.BootArtifactsSourceS3, s3Set},
	}
	for _, m := range members {
		switch {
		case m.typ == sourceType && !m.set:
			allErrs = append(allErrs, field.Required(path.Child(m.name),
				fmt.Sprintf("%s is required when type=%s", m.name, sourceType)))
		case m.typ != sourceType && m.set:
			allErrs = append(allErrs, field.Forbidden(path.Child(m.name),
				fmt.Sprintf("%s must not be set when type=%s", m.name, sourceType)))
		}
	}
	return allErrs
}

func validateBootArtifactsSource(src infrastructurev1alpha1.BootArtifactsSource, path *field.Path) field.ErrorList {
	allErrs := validateSourceMembers(path, src.Type, src.HTTPS != nil, src.OCI != nil, src.S3 != nil)
	if h := src.HTTPS; h != nil && src.Type == infrastructurev1alpha1.BootArtifactsSourceHTTPS {
		p := path.Child("https")
		// The boot artifact HTTPS resolver refuses plain http outright.
		allErrs = appendURLError(allErrs, p.Child("kernelURL"), h.KernelURL, "https")
		allErrs = appendURLError(allErrs, p.Child("initramfsURL"), h.InitramfsURL, "https")
		allErrs = appendSHA256Error(allErrs, p.Child("kernelSHA256"), h.KernelSHA256)
		allErrs = appendSHA256Error(allErrs, p.Child("initramfsSHA256"), h.InitramfsSHA256)
	}
	if o := src.OCI; o != nil && src.Type == infrastructurev1alpha1.BootArtifactsSourceOCI {
		p := path.Child("oci")
		allErrs = appendOCIReferenceError(allErrs, p.Child("reference"), o.Reference)
		allErrs = appendSHA256Error(allErrs, p.Child("kernelSHA256"), o.KernelSHA256)
		allErrs = appendSHA256Error(allErrs, p.Child("initramfsSHA256"), o.InitramfsSHA256)
	}
	if s := src.S3; s != nil && src.Type == infrastructurev1alpha1.BootArtifactsSourceS3 {
		p := path.Child("s3")
		allErrs = appendS3EndpointError(allErrs, p.Child("endpoint"), s.Endpoint)
		allErrs = appendSHA256Error(allErrs, p.Child("kernelSHA256"), s.KernelSHA256)
		allErrs = appendSHA256Error(allErrs, p.Child("initramfsSHA256"), s.InitramfsSHA256)
	}
	return allErrs
}

func validateBaseImageSource(src infrastructurev1alpha1.BaseImageSource, path *field.Path) field.ErrorList {
	allErrs := validateSourceMembers(path, src.Type, src.HTTPS != nil, src.OCI != nil, src.S3 != nil)
	if h := src.HTTPS; h != nil && src.Type == infrastructurev1alpha1.BootArtifactsSourceHTTPS {
		p := path.Child("https")
		// Plain http is accepted here and gated by the controller's
		// --allow-insecure-image-transport policy instead.
		allErrs = appendURLError(allErrs, p.Child("url"), h.URL, "https", "http")
		allErrs = appendSHA256Error(allErrs, p.Child("sha256"), h.SHA256)
	}
	if o := src.OCI; o != nil && src.Type == infrastructurev1alpha1.BootArtifactsSourceOCI {
		p := path.Child("oci")
		allErrs = appendOCIReferenceError(allErrs, p.Child("reference"), o.Reference)
		allErrs = appendSHA256Error(allErrs, p.Child("sha256"), o.SHA256)
	}
	if s := src.S3; s != nil && src.Type == infrastructurev1alpha1.BootArtifactsSourceS3 {
		p := path.Child("s3")
		allErrs = appendS3EndpointError(allErrs, p.Child("endpoint"), s.Endpoint)
		allErrs = appendSHA256Error(allErrs, p.Child("sha256"), s.SHA256)
	}
	return allErrs
}

func appendURLError(allErrs field.ErrorList, path *field.Path, raw string, schemes ...string) field.ErrorList {
	u, err := url.Parse(raw)
	if err != nil {
		return append(allErrs, field.Invalid(path, raw, fmt.Sprintf("invalid URL: %v", err)))
	}
	if u.Host == "" {
		return append(allErrs, field.Invalid(path, raw, "must be an absolute URL with a host"))
	}
	for _, s := range schemes {
		if strings.EqualFold(u.Scheme, s) {
			return allErrs
		}
	}
	return append(allErrs, field.Invalid(path, raw,
		fmt.Sprintf("scheme must be one of: %s", strings.Join(schemes, ", "))))
}

func appendSHA256Error(allErrs field.ErrorList, path *field.Path, digest string) field.ErrorList {
	if digest == "" || sha256HexPattern.MatchString(digest) {
		return allErrs
	}
	return append(allErrs, field.Invalid(path, digest, "must be a 64-character hex sha256 digest, without a sha256: prefix"))
}

// appendOCIReferenceError rejects references carrying a URL scheme. ORAS takes
// "registry/repository:tag" or "...@sha256:..."; a scheme makes the registry
// host unparseable and fails only once a machine is being provisioned.
func appendOCIReferenceError(allErrs field.ErrorList, path *field.Path, ref string) field.ErrorList {
	if ref == "" {
		return append(allErrs, field.Required(path, "an OCI reference is required"))
	}
	if strings.Contains(ref, "://") {
		return append(allErrs, field.Invalid(path, ref,
			"must be a registry reference (registry/repository:tag), not a URL; use plainHTTP for an http registry"))
	}
	return allErrs
}

// appendS3EndpointError rejects endpoints carrying a URL scheme. The S3
// client takes "host[:port]" and chooses the scheme from the insecure field.
func appendS3EndpointError(allErrs field.ErrorList, path *field.Path, endpoint string) field.ErrorList {
	if endpoint == "" {
		return append(allErrs, field.Required(path, "an S3 endpoint is required"))
	}
	if strings.Contains(endpoint, "://") || strings.Contains(endpoint, "/") {
		return append(allErrs, field.Invalid(path, endpoint,
			"must be host[:port] without a scheme or path; set insecure for plain http"))
	}
	return allErrs
}

// insecureTransportWarnings reports image sources the controller will refuse
// to fetch because the manager runs without --allow-insecure-image-transport.
// They are warnings, not errors: the policy is the controller's to enforce,
// and the operator may be about to enable the flag.
func (v *LibvirtClusterCustomValidator) insecureTransportWarnings(spec infrastructurev1alpha1.LibvirtClusterSpec) admission.Warnings {
	if v.AllowInsecureImageTransport {
		return nil
	}
	var insecure []string
	if ba := spec.BootArtifacts; ba != nil {
		if o := ba.Source.OCI; o != nil {
			insecure = appendIf(insecure, o.PlainHTTP, "spec.bootArtifacts.source.oci.plainHTTP")
			insecure = appendIf(insecure, o.InsecureSkipTLSVerify, "spec.bootArtifacts.source.oci.insecureSkipTLSVerify")
		}
		if s := ba.Source.S3; s != nil {
			insecure = appendIf(insecure, s.Insecure, "spec.bootArtifacts.source.s3.insecure")
			insecure = appendIf(insecure, s.InsecureSkipTLSVerify, "spec.bootArtifacts.source.s3.insecureSkipTLSVerify")
		}
	}
	if bi := spec.BaseImage; bi != nil {
		if h := bi.Source.HTTPS; h != nil {
			insecure = appendIf(insecure, strings.HasPrefix(strings.ToLower(h.URL), "http://"), "spec.baseImage.source.https.url (plain http)")
			insecure = appendIf(insecure, h.InsecureSkipTLSVerify, "spec.baseImage.source.https.insecureSkipTLSVerify")
		}
		if o := bi.Source.OCI; o != nil {
			insecure = appendIf(insecure, o.PlainHTTP, "spec.baseImage.source.oci.plainHTTP")
			insecure = appendIf(insecure, o.InsecureSkipTLSVerify, "spec.baseImage.source.oci.insecureSkipTLSVerify")
		}
		if s := bi.Source.S3; s != nil {
			insecure = appendIf(insecure, s.Insecure, "spec.baseImage.source.s3.insecure")
			insecure = appendIf(insecure, s.InsecureSkipTLSVerify, "spec.baseImage.source.s3.insecureSkipTLSVerify")
		}
	}
	if len(insecure) == 0 {
		return nil
	}
	return admission.Warnings{fmt.Sprintf(
		"%s disables transport authentication; the controller will refuse to fetch from this source "+
			"unless the manager runs with --allow-insecure-image-transport",
		strings.Join(insecure, ", "))}
}

func appendIf(list []string, cond bool, s string) []string {
	if cond {
		return append(list, s)
	}
	return list
}

func invalidCluster(obj *infrastructurev1alpha1.LibvirtCluster, allErrs field.ErrorList) error {
	return apierrors.NewInvalid(
		schema.GroupKind{
			Group: infrastructurev1alpha1.GroupVersion.Group,
			Kind:  "LibvirtCluster",
		},
		obj.Name,
		allErrs,
	)
}
