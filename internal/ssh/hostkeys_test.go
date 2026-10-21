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

package ssh

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"errors"
	"net"
	"strings"
	"testing"

	gossh "golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

// A stock sshd has an ECDSA, an ed25519 and an RSA host key, and x/crypto
// prefers ECDSA, so pinning the ed25519 fingerprint (as the setup playbook
// prints) used to fail with a host key mismatch. Whichever key is pinned,
// the client must find it.
func TestNewSSHClient_FindsThePinnedKeyAmongTheHostsKeys(t *testing.T) {
	hostKeys := map[string]gossh.Signer{
		"ed25519": newSigner(t, mustEd25519(t)),
		"ecdsa":   newSigner(t, mustECDSA(t)),
		"rsa":     newSigner(t, mustRSA(t)),
	}
	addr := startSSHServer(t, hostKeys["ecdsa"], hostKeys["ed25519"], hostKeys["rsa"])

	for name, signer := range hostKeys {
		t.Run(name, func(t *testing.T) {
			client, err := NewSSHClient(context.Background(),
				libvirtHost(addr, gossh.FingerprintSHA256(signer.PublicKey())), clientSecret(t))
			if err != nil {
				t.Fatalf("connecting with the %s host key pinned: %v", name, err)
			}
			_ = client.Close()
		})
	}
}

// A host with only some key types is still found, skipping the types it
// lacks.
func TestNewSSHClient_SkipsKeyTypesTheHostLacks(t *testing.T) {
	signer := newSigner(t, mustRSA(t))
	addr := startSSHServer(t, signer)

	client, err := NewSSHClient(context.Background(),
		libvirtHost(addr, gossh.FingerprintSHA256(signer.PublicKey())), clientSecret(t))
	if err != nil {
		t.Fatalf("connecting to an RSA-only host: %v", err)
	}
	_ = client.Close()
}

func TestNewSSHClient_RejectsAHostWithoutThePinnedKey(t *testing.T) {
	addr := startSSHServer(t, newSigner(t, mustECDSA(t)), newSigner(t, mustEd25519(t)))
	other := newSigner(t, mustEd25519(t))

	_, err := NewSSHClient(context.Background(),
		libvirtHost(addr, gossh.FingerprintSHA256(other.PublicKey())), clientSecret(t))
	if !errors.Is(err, errHostKeyMismatch) {
		t.Fatalf("expected a host key mismatch, got %v", err)
	}
	// Every key the host showed is reported, so the user can see which
	// fingerprints it does have.
	for _, keyType := range []string{gossh.KeyAlgoED25519, gossh.KeyAlgoECDSA256} {
		if !strings.Contains(err.Error(), keyType) {
			t.Errorf("expected the error to mention the host's %s key: %v", keyType, err)
		}
	}
}

// startSSHServer serves SSH on a loopback port with the given host keys,
// accepting any client key, and returns its address.
func startSSHServer(t *testing.T, hostKeys ...gossh.Signer) string {
	t.Helper()
	config := &gossh.ServerConfig{
		PublicKeyCallback: func(gossh.ConnMetadata, gossh.PublicKey) (*gossh.Permissions, error) {
			return &gossh.Permissions{}, nil
		},
	}
	for _, key := range hostKeys {
		config.AddHostKey(key)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				sshConn, chans, reqs, err := gossh.NewServerConn(conn, config)
				if err != nil {
					return
				}
				defer func() { _ = sshConn.Close() }()
				go gossh.DiscardRequests(reqs)
				for ch := range chans {
					_ = ch.Reject(gossh.Prohibited, "no channels")
				}
			}()
		}
	}()
	return listener.Addr().String()
}

func libvirtHost(addr, fingerprint string) *infrav1.LibvirtHost {
	return &infrav1.LibvirtHost{Spec: infrav1.LibvirtHostSpec{
		URI:                "qemu+ssh://caplv@" + addr + "/system",
		HostKeyFingerprint: fingerprint,
	}}
}

func clientSecret(t *testing.T) *corev1.Secret {
	t.Helper()
	block, err := gossh.MarshalPrivateKey(mustEd25519(t), "")
	if err != nil {
		t.Fatalf("marshal client key: %v", err)
	}
	return &corev1.Secret{Data: map[string][]byte{"ssh-privatekey": pem.EncodeToMemory(block)}}
}

func newSigner(t *testing.T, key crypto.Signer) gossh.Signer {
	t.Helper()
	signer, err := gossh.NewSignerFromSigner(key)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	return signer
}

func mustEd25519(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	return key
}

func mustECDSA(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ECDSA key: %v", err)
	}
	return key
}

func mustRSA(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return key
}
