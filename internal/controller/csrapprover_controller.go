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
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"slices"
	"strings"

	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	infrav1 "github.com/atgreen/caplv/api/v1alpha1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	kubeletClientSignerName   = "kubernetes.io/kube-apiserver-client-kubelet"
	kubeletServingSignerName  = "kubernetes.io/kubelet-serving"
	systemNodePrefix          = "system:node:"
	systemNodesGroup          = "system:nodes"
	systemBootstrappersGroup  = "system:bootstrappers"
	openshiftNodeBootstrapper = "system:serviceaccount:openshift-machine-config-operator:node-bootstrapper"
)

// CSRApproverReconciler watches for pending CertificateSigningRequests and
// auto-approves those that match a CAPI Machine backed by a LibvirtMachine.
type CSRApproverReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=certificates.k8s.io,resources=certificatesigningrequests,verbs=get;list;watch
// +kubebuilder:rbac:groups=certificates.k8s.io,resources=certificatesigningrequests/approval,verbs=update
// +kubebuilder:rbac:groups=certificates.k8s.io,resources=signers,resourceNames=kubernetes.io/kube-apiserver-client-kubelet;kubernetes.io/kubelet-serving,verbs=approve
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirtmachines,verbs=get;list;watch

func (r *CSRApproverReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("csr", req.Name)

	csr := &certificatesv1.CertificateSigningRequest{}
	if err := r.Get(ctx, req.NamespacedName, csr); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Skip if already approved or denied.
	if isApprovedOrDenied(csr) {
		return ctrl.Result{}, nil
	}

	// Only handle kubelet client and serving CSRs.
	if csr.Spec.SignerName != kubeletClientSignerName &&
		csr.Spec.SignerName != kubeletServingSignerName {
		return ctrl.Result{}, nil
	}

	// Extract the node name from the CSR.
	nodeName := nodeNameFromCSR(csr)
	if nodeName == "" {
		return ctrl.Result{}, nil
	}

	// Check if this node name matches a CAPI Machine backed by a LibvirtMachine.
	lm := r.libvirtMachineForNode(ctx, nodeName)
	if lm == nil {
		return ctrl.Result{}, nil
	}
	if !shouldApproveKubeletCSR(csr, nodeName, machineNodeIPs(lm)) {
		log.Info("CSR did not match CAPLV kubelet approval policy", "node", nodeName, "signer", csr.Spec.SignerName)
		return ctrl.Result{}, nil
	}

	// Approve the CSR.
	log.Info("Auto-approving CSR for CAPLV-managed node", "node", nodeName, "signer", csr.Spec.SignerName)
	csr.Status.Conditions = append(csr.Status.Conditions, certificatesv1.CertificateSigningRequestCondition{
		Type:           certificatesv1.CertificateApproved,
		Status:         corev1.ConditionTrue,
		Reason:         "CAPLVAutoApproved",
		Message:        fmt.Sprintf("CSR auto-approved by CAPLV for managed node %s", nodeName),
		LastUpdateTime: metav1.Now(),
	})

	if err := r.Client.SubResource("approval").Update(ctx, csr); err != nil {
		log.Error(err, "Failed to approve CSR")
		return ctrl.Result{}, err
	}

	log.Info("CSR approved", "node", nodeName)
	return ctrl.Result{}, nil
}

// libvirtMachineForNode returns the LibvirtMachine that provisioned nodeName,
// or nil when the node is not CAPLV-managed. The returned object carries the
// addresses a serving certificate for this node is allowed to claim.
func (r *CSRApproverReconciler) libvirtMachineForNode(ctx context.Context, nodeName string) *infrav1.LibvirtMachine {
	// List all Machines across all namespaces.
	machineList := &clusterv1.MachineList{}
	if err := r.List(ctx, machineList); err != nil {
		return nil
	}

	for i := range machineList.Items {
		machine := &machineList.Items[i]

		// Check if the infrastructure ref points to a LibvirtMachine.
		if machine.Spec.InfrastructureRef.GroupVersionKind().Kind != "LibvirtMachine" {
			continue
		}

		// Look up the LibvirtMachine to get the domain name.
		lm := &infrav1.LibvirtMachine{}
		lmKey := client.ObjectKey{
			Namespace: machine.Spec.InfrastructureRef.Namespace,
			Name:      machine.Spec.InfrastructureRef.Name,
		}
		if err := r.Get(ctx, lmKey, lm); err != nil {
			continue
		}

		// The domain name is <namespace>-<cluster>-<machine> which is
		// also the hostname we inject into ignition.
		domainName := fmt.Sprintf("%s-%s-%s", lm.Namespace, machine.Labels["cluster.x-k8s.io/cluster-name"], lm.Name)
		if domainName == nodeName {
			return lm
		}
	}

	return nil
}

// machineNodeIPs returns the IP addresses this machine was provisioned with,
// parsed out of spec.network.addresses (CIDR notation).
func machineNodeIPs(lm *infrav1.LibvirtMachine) []net.IP {
	ips := make([]net.IP, 0, len(lm.Spec.Network.Addresses))
	for _, addr := range lm.Spec.Network.Addresses {
		// Addresses are CIDRs (webhook-validated); tolerate a bare IP too.
		if ip, _, err := net.ParseCIDR(addr); err == nil {
			ips = append(ips, ip)
			continue
		}
		if ip := net.ParseIP(addr); ip != nil {
			ips = append(ips, ip)
		}
	}
	return ips
}

// nodeNameFromCSR extracts the node name from a CSR.
// It checks the username first (kubelet serving/renewal CSRs use
// "system:node:<nodename>"), then falls back to parsing the CN from
// the CSR request (bootstrap CSRs use the node-bootstrapper SA as
// the username but set CN=system:node:<nodename> in the request).
func nodeNameFromCSR(csr *certificatesv1.CertificateSigningRequest) string {
	// Check username first (serving and renewal CSRs).
	if strings.HasPrefix(csr.Spec.Username, systemNodePrefix) {
		return csr.Spec.Username[len(systemNodePrefix):]
	}

	// Fall back to parsing the CSR request CN (bootstrap CSRs).
	req, err := parseCSRRequest(csr)
	if err != nil {
		return ""
	}
	if strings.HasPrefix(req.Subject.CommonName, systemNodePrefix) {
		return req.Subject.CommonName[len(systemNodePrefix):]
	}

	return ""
}

// shouldApproveKubeletCSR decides whether a kubelet CSR for nodeName may be
// auto-approved. allowedIPs is the set of addresses the machine behind nodeName
// was provisioned with; a serving certificate may not claim any other.
func shouldApproveKubeletCSR(
	csr *certificatesv1.CertificateSigningRequest,
	nodeName string,
	allowedIPs []net.IP,
) bool {
	req, err := parseCSRRequest(csr)
	if err != nil {
		return false
	}
	if req.Subject.CommonName != systemNodePrefix+nodeName || !containsString(req.Subject.Organization, systemNodesGroup) {
		return false
	}

	switch csr.Spec.SignerName {
	case kubeletClientSignerName:
		return validKubeletClientRequester(csr, nodeName) && hasOnlyUsages(csr.Spec.Usages,
			certificatesv1.UsageDigitalSignature,
			certificatesv1.UsageKeyEncipherment,
			certificatesv1.UsageClientAuth,
		) && containsUsage(csr.Spec.Usages, certificatesv1.UsageClientAuth)
	case kubeletServingSignerName:
		return validKubeletServingRequester(csr, nodeName) &&
			validKubeletServingSANs(req, nodeName, allowedIPs) &&
			hasOnlyUsages(csr.Spec.Usages,
				certificatesv1.UsageDigitalSignature,
				certificatesv1.UsageKeyEncipherment,
				certificatesv1.UsageServerAuth,
			) &&
			containsUsage(csr.Spec.Usages, certificatesv1.UsageServerAuth)
	default:
		return false
	}
}

func parseCSRRequest(csr *certificatesv1.CertificateSigningRequest) (*x509.CertificateRequest, error) {
	block, _ := pem.Decode(csr.Spec.Request)
	if block == nil {
		return nil, fmt.Errorf("CSR request is not PEM encoded")
	}
	return x509.ParseCertificateRequest(block.Bytes)
}

func validKubeletClientRequester(csr *certificatesv1.CertificateSigningRequest, nodeName string) bool {
	if csr.Spec.Username == systemNodePrefix+nodeName && containsString(csr.Spec.Groups, systemNodesGroup) {
		return true
	}
	if strings.HasPrefix(csr.Spec.Username, "system:bootstrap:") && containsGroupPrefix(csr.Spec.Groups, systemBootstrappersGroup) {
		return true
	}
	if csr.Spec.Username == openshiftNodeBootstrapper {
		return true
	}
	return false
}

func validKubeletServingRequester(csr *certificatesv1.CertificateSigningRequest, nodeName string) bool {
	return csr.Spec.Username == systemNodePrefix+nodeName && containsString(csr.Spec.Groups, systemNodesGroup)
}

// validKubeletServingSANs checks that every SAN in a kubelet-serving CSR names
// only this node.
//
// Every IP must be one the machine was actually provisioned with. Accepting any
// routable address instead would let a node that holds its own kubelet
// credential mint a cluster-CA-signed server certificate for a peer's IP (or a
// service/API address) and impersonate it to anything that trusts the cluster
// CA — which is why kube-controller-manager never auto-approves this signer and
// why OpenShift's own approver checks SANs against the Machine's addresses.
// An empty allowedIPs therefore permits no IP SANs at all, rather than all of
// them.
func validKubeletServingSANs(req *x509.CertificateRequest, nodeName string, allowedIPs []net.IP) bool {
	if len(req.EmailAddresses) > 0 || len(req.URIs) > 0 {
		return false
	}
	for _, dnsName := range req.DNSNames {
		if dnsName != nodeName && !strings.HasPrefix(dnsName, nodeName+".") {
			return false
		}
	}
	for _, ip := range req.IPAddresses {
		if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() {
			return false
		}
		if !containsIP(allowedIPs, ip) {
			return false
		}
	}
	return true
}

// containsIP reports whether ip is in the allow-list, comparing by value so the
// 4-byte and 16-byte encodings of the same IPv4 address match.
func containsIP(allowed []net.IP, ip net.IP) bool {
	for _, a := range allowed {
		if a.Equal(ip) {
			return true
		}
	}
	return false
}

func hasOnlyUsages(actual []certificatesv1.KeyUsage, allowed ...certificatesv1.KeyUsage) bool {
	for _, usage := range actual {
		if !containsUsage(allowed, usage) {
			return false
		}
	}
	return true
}

func containsUsage(usages []certificatesv1.KeyUsage, usage certificatesv1.KeyUsage) bool {
	return slices.Contains(usages, usage)
}

func containsString(values []string, value string) bool {
	return slices.Contains(values, value)
}

func containsGroupPrefix(groups []string, prefix string) bool {
	for _, group := range groups {
		if group == prefix || strings.HasPrefix(group, prefix+":") {
			return true
		}
	}
	return false
}

func isApprovedOrDenied(csr *certificatesv1.CertificateSigningRequest) bool {
	for _, c := range csr.Status.Conditions {
		if c.Type == certificatesv1.CertificateApproved || c.Type == certificatesv1.CertificateDenied {
			return true
		}
	}
	return false
}

func (r *CSRApproverReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&certificatesv1.CertificateSigningRequest{}).
		Named("csrapprover").
		Complete(r)
}
