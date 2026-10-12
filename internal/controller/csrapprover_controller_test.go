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
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"net"
	"net/url"
	"testing"

	certificatesv1 "k8s.io/api/certificates/v1"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

const testNodeName = "default-spot-worker01"

// testNodeIPs is the address set the machine behind testNodeName was
// provisioned with; a serving certificate may claim these and nothing else.
var testNodeIPs = []net.IP{net.ParseIP("192.0.2.10")}

func TestShouldApproveKubeletCSR_BootstrapClientCSR(t *testing.T) {
	csr := kubeletCSR(kubeletClientSignerName, "system:bootstrap:abc123",
		[]string{systemBootstrappersGroup},
		csrTemplate(),
		[]certificatesv1.KeyUsage{
			certificatesv1.UsageDigitalSignature,
			certificatesv1.UsageKeyEncipherment,
			certificatesv1.UsageClientAuth,
		})

	if !shouldApproveKubeletCSR(csr, testNodeName, testNodeIPs) {
		t.Fatal("expected bootstrap client CSR to be approved")
	}
}

func TestShouldApproveKubeletCSR_OpenShiftBootstrapClientCSR(t *testing.T) {
	csr := kubeletCSR(kubeletClientSignerName, openshiftNodeBootstrapper,
		[]string{"system:serviceaccounts", "system:authenticated"},
		csrTemplate(),
		[]certificatesv1.KeyUsage{
			certificatesv1.UsageDigitalSignature,
			certificatesv1.UsageKeyEncipherment,
			certificatesv1.UsageClientAuth,
		})

	if !shouldApproveKubeletCSR(csr, testNodeName, testNodeIPs) {
		t.Fatal("expected OpenShift node-bootstrapper client CSR to be approved")
	}
}

func TestShouldApproveKubeletCSR_RejectsForgedClientCSRRequester(t *testing.T) {
	csr := kubeletCSR(kubeletClientSignerName, "system:serviceaccount:default:attacker",
		[]string{"system:serviceaccounts", "system:authenticated"},
		csrTemplate(),
		[]certificatesv1.KeyUsage{
			certificatesv1.UsageDigitalSignature,
			certificatesv1.UsageKeyEncipherment,
			certificatesv1.UsageClientAuth,
		})

	if shouldApproveKubeletCSR(csr, testNodeName, testNodeIPs) {
		t.Fatal("expected forged client CSR requester to be rejected")
	}
}

func TestShouldApproveKubeletCSR_RejectsNodeMismatch(t *testing.T) {
	csr := kubeletCSR(kubeletClientSignerName, "system:node:other-node",
		[]string{systemNodesGroup},
		csrTemplate(),
		[]certificatesv1.KeyUsage{
			certificatesv1.UsageDigitalSignature,
			certificatesv1.UsageKeyEncipherment,
			certificatesv1.UsageClientAuth,
		})

	if shouldApproveKubeletCSR(csr, testNodeName, testNodeIPs) {
		t.Fatal("expected mismatched node requester to be rejected")
	}
}

func TestShouldApproveKubeletCSR_RejectsWrongClientUsage(t *testing.T) {
	csr := kubeletCSR(kubeletClientSignerName, "system:bootstrap:abc123",
		[]string{systemBootstrappersGroup},
		csrTemplate(),
		[]certificatesv1.KeyUsage{certificatesv1.UsageServerAuth})

	if shouldApproveKubeletCSR(csr, testNodeName, testNodeIPs) {
		t.Fatal("expected client CSR with server usage to be rejected")
	}
}

func TestShouldApproveKubeletCSR_ServingCSR(t *testing.T) {
	template := csrTemplate()
	template.DNSNames = []string{testNodeName, testNodeName + ".example.com"}
	template.IPAddresses = []net.IP{net.ParseIP("192.0.2.10")}
	csr := kubeletCSR(kubeletServingSignerName, systemNodePrefix+testNodeName,
		[]string{systemNodesGroup},
		template,
		[]certificatesv1.KeyUsage{
			certificatesv1.UsageDigitalSignature,
			certificatesv1.UsageKeyEncipherment,
			certificatesv1.UsageServerAuth,
		})

	if !shouldApproveKubeletCSR(csr, testNodeName, testNodeIPs) {
		t.Fatal("expected serving CSR to be approved")
	}
}

func TestShouldApproveKubeletCSR_RejectsServingCSRFromBootstrapper(t *testing.T) {
	csr := kubeletCSR(kubeletServingSignerName, "system:bootstrap:abc123",
		[]string{systemBootstrappersGroup},
		csrTemplate(),
		[]certificatesv1.KeyUsage{
			certificatesv1.UsageDigitalSignature,
			certificatesv1.UsageKeyEncipherment,
			certificatesv1.UsageServerAuth,
		})

	if shouldApproveKubeletCSR(csr, testNodeName, testNodeIPs) {
		t.Fatal("expected serving CSR from bootstrapper to be rejected")
	}
}

func TestShouldApproveKubeletCSR_RejectsServingCSRWithForeignSAN(t *testing.T) {
	template := csrTemplate()
	template.DNSNames = []string{"api.internal.example.com"}
	template.EmailAddresses = []string{"attacker@example.com"}
	template.URIs = []*url.URL{{Scheme: "spiffe", Host: "example.com", Path: "/attacker"}}
	csr := kubeletCSR(kubeletServingSignerName, systemNodePrefix+testNodeName,
		[]string{systemNodesGroup},
		template,
		[]certificatesv1.KeyUsage{
			certificatesv1.UsageDigitalSignature,
			certificatesv1.UsageKeyEncipherment,
			certificatesv1.UsageServerAuth,
		})

	if shouldApproveKubeletCSR(csr, testNodeName, testNodeIPs) {
		t.Fatal("expected serving CSR with foreign SANs to be rejected")
	}
}

func TestNodeNameFromCSR_FallsBackToCSRCommonName(t *testing.T) {
	csr := kubeletCSR(kubeletClientSignerName, "system:bootstrap:abc123",
		[]string{systemBootstrappersGroup},
		csrTemplate(),
		[]certificatesv1.KeyUsage{certificatesv1.UsageClientAuth})

	if got := nodeNameFromCSR(csr); got != testNodeName {
		t.Fatalf("nodeNameFromCSR() = %q, want %q", got, testNodeName)
	}
}

func kubeletCSR(
	signerName string,
	username string,
	groups []string,
	template *x509.CertificateRequest,
	usages []certificatesv1.KeyUsage,
) *certificatesv1.CertificateSigningRequest {
	return &certificatesv1.CertificateSigningRequest{
		Spec: certificatesv1.CertificateSigningRequestSpec{
			Request:    mustCreateCSR(template),
			SignerName: signerName,
			Usages:     usages,
			Username:   username,
			Groups:     groups,
		},
	}
}

func csrTemplate() *x509.CertificateRequest {
	return &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName:   systemNodePrefix + testNodeName,
			Organization: []string{systemNodesGroup},
		},
	}
}

func mustCreateCSR(template *x509.CertificateRequest) []byte {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, template, key)
	if err != nil {
		panic(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
}

// --- kubelet-serving SAN binding ---
//
// A serving certificate must claim only addresses the machine was actually
// provisioned with. Without this, a node holding its own kubelet credential can
// mint a cluster-CA-signed server certificate for a peer's IP and impersonate
// it to anything that trusts the cluster CA.

func servingCSRWithIPs(ips ...net.IP) *certificatesv1.CertificateSigningRequest {
	template := csrTemplate()
	template.DNSNames = []string{testNodeName}
	template.IPAddresses = ips
	return kubeletCSR(kubeletServingSignerName, systemNodePrefix+testNodeName,
		[]string{systemNodesGroup},
		template,
		[]certificatesv1.KeyUsage{
			certificatesv1.UsageDigitalSignature,
			certificatesv1.UsageKeyEncipherment,
			certificatesv1.UsageServerAuth,
		})
}

func TestShouldApproveKubeletCSR_RejectsServingCSRForForeignIP(t *testing.T) {
	csr := servingCSRWithIPs(net.ParseIP("192.0.2.99")) // a peer's address
	if shouldApproveKubeletCSR(csr, testNodeName, testNodeIPs) {
		t.Fatal("expected a serving CSR claiming an unprovisioned IP to be rejected")
	}
}

func TestShouldApproveKubeletCSR_RejectsServingCSRMixingOwnAndForeignIP(t *testing.T) {
	csr := servingCSRWithIPs(net.ParseIP("192.0.2.10"), net.ParseIP("192.0.2.99"))
	if shouldApproveKubeletCSR(csr, testNodeName, testNodeIPs) {
		t.Fatal("every IP SAN must be allowed, not just the first")
	}
}

func TestShouldApproveKubeletCSR_RejectsServingCSRWhenMachineHasNoAddresses(t *testing.T) {
	csr := servingCSRWithIPs(net.ParseIP("192.0.2.10"))
	if shouldApproveKubeletCSR(csr, testNodeName, nil) {
		t.Fatal("an empty allow-list must permit no IP SANs, not all of them")
	}
}

func TestShouldApproveKubeletCSR_AllowsServingCSRWithNoIPSANs(t *testing.T) {
	csr := servingCSRWithIPs()
	if !shouldApproveKubeletCSR(csr, testNodeName, testNodeIPs) {
		t.Fatal("a serving CSR with no IP SANs should still be approved")
	}
}

// IPv4 addresses have a 4-byte and a 16-byte encoding; matching must not
// depend on which one the requester used.
func TestValidKubeletServingSANs_MatchesIPv4AcrossEncodings(t *testing.T) {
	req := &x509.CertificateRequest{IPAddresses: []net.IP{net.ParseIP("192.0.2.10").To4()}}
	allowed := []net.IP{net.ParseIP("192.0.2.10").To16()}
	if !validKubeletServingSANs(req, testNodeName, allowed) {
		t.Fatal("4-byte and 16-byte encodings of the same IPv4 address should match")
	}
}

func TestMachineNodeIPs_ParsesCIDRsAndBareIPs(t *testing.T) {
	lm := &infrav1.LibvirtMachine{
		Spec: infrav1.LibvirtMachineSpec{
			Network: infrav1.NetworkSpec{
				Addresses: []string{"192.0.2.10/24", "198.51.100.7", "not-an-address"},
			},
		},
	}
	got := machineNodeIPs(lm)
	if len(got) != 2 {
		t.Fatalf("expected 2 parsed addresses, got %d (%v)", len(got), got)
	}
	if !got[0].Equal(net.ParseIP("192.0.2.10")) || !got[1].Equal(net.ParseIP("198.51.100.7")) {
		t.Errorf("unexpected addresses: %v", got)
	}
}
