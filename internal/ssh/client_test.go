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
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"

	gossh "golang.org/x/crypto/ssh"
)

func TestParseLibvirtURI_StandardSSH(t *testing.T) {
	user, host, port, err := ParseLibvirtURI("qemu+ssh://root@host.example.com/system")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user != "root" {
		t.Errorf("expected user 'root', got %q", user)
	}
	if host != "host.example.com" {
		t.Errorf("expected host 'host.example.com', got %q", host)
	}
	if port != 22 {
		t.Errorf("expected port 22, got %d", port)
	}
}

func TestParseLibvirtURI_CustomPort(t *testing.T) {
	user, host, port, err := ParseLibvirtURI("qemu+ssh://admin@host:2222/system")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user != "admin" {
		t.Errorf("expected user 'admin', got %q", user)
	}
	if host != "host" {
		t.Errorf("expected host 'host', got %q", host)
	}
	if port != 2222 {
		t.Errorf("expected port 2222, got %d", port)
	}
}

func TestParseLibvirtURI_NotSSH(t *testing.T) {
	_, _, _, err := ParseLibvirtURI("qemu:///system")
	if err == nil {
		t.Error("expected error for non-SSH URI, got nil")
	}
}

func TestParseLibvirtURI_InvalidURI(t *testing.T) {
	_, _, _, err := ParseLibvirtURI("://not a valid uri")
	if err == nil {
		t.Error("expected error for invalid URI, got nil")
	}
}

// --- host key policy ---
//
// An empty spec.hostKeyFingerprint used to fall through to
// InsecureIgnoreHostKey, which accepts any host key and therefore any
// impersonator. The SSH channel carries the worker Ignition config, so it must
// fail closed unless the operator passed --allow-insecure-host-keys.

func TestHostKeyCallback_RefusesUnpinnedHostByDefault(t *testing.T) {
	cb, err := HostKeyCallback("", false)
	if err == nil {
		t.Fatal("expected an empty fingerprint to be refused")
	}
	if cb != nil {
		t.Error("expected no callback to be returned on refusal")
	}
}

func TestHostKeyCallback_AllowsUnpinnedHostWhenOptedIn(t *testing.T) {
	cb, err := HostKeyCallback("", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cb == nil {
		t.Error("expected an insecure callback when opted in")
	}
}

func TestHostKeyCallback_PinnedFingerprintDoesNotNeedOptIn(t *testing.T) {
	cb, err := HostKeyCallback("SHA256:abc123", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cb == nil {
		t.Fatal("expected a verifying callback")
	}
}

func TestVerifyHostKey_RejectsMismatchedFingerprint(t *testing.T) {
	key := testHostKey(t)
	cb := VerifyHostKey("SHA256:definitely-not-the-right-fingerprint")
	if err := cb("host.example.com:22", &net.TCPAddr{}, key); err == nil {
		t.Fatal("expected a mismatched host key to be rejected")
	}
}

func TestVerifyHostKey_AcceptsMatchingFingerprint(t *testing.T) {
	key := testHostKey(t)
	cb := VerifyHostKey(gossh.FingerprintSHA256(key))
	if err := cb("host.example.com:22", &net.TCPAddr{}, key); err != nil {
		t.Fatalf("expected the matching host key to be accepted: %v", err)
	}
}

// An empty fingerprint reaching VerifyHostKey directly must reject every key,
// never accept one — HostKeyCallback owns the decision about what "unpinned"
// means.
func TestVerifyHostKey_EmptyFingerprintRejectsEverything(t *testing.T) {
	key := testHostKey(t)
	if err := VerifyHostKey("")("host.example.com:22", &net.TCPAddr{}, key); err == nil {
		t.Fatal("expected an empty fingerprint to reject the host key")
	}
}

func testHostKey(t *testing.T) gossh.PublicKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	return signer.PublicKey()
}
