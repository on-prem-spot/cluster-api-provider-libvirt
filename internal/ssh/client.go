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
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	gossh "golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

const defaultSSHPort = 22

// errHostKeyMismatch marks a host key whose fingerprint is not the pinned one.
var errHostKeyMismatch = errors.New("host key mismatch")

// hostKeyAlgorithmGroups lists the host key algorithms by key type, ed25519
// first since it is the key whose fingerprint the setup playbook and docs
// pin. A host usually has a key of each type, but a fingerprint does not say
// which one it is for, and the server shows only the key of the algorithm
// the handshake settles on (x/crypto prefers ECDSA and RSA to ed25519). So
// a pinned host is offered one key type per handshake until its key matches.
var hostKeyAlgorithmGroups = [][]string{
	{gossh.KeyAlgoED25519},
	{gossh.KeyAlgoECDSA256, gossh.KeyAlgoECDSA384, gossh.KeyAlgoECDSA521},
	{gossh.KeyAlgoRSASHA512, gossh.KeyAlgoRSASHA256},
}

// NewSSHClient creates an SSH client connection to the libvirt host using
// credentials from the provided Secret. The host's SSH host key must be pinned
// via spec.hostKeyFingerprint; see NewSSHClientFactory to relax that.
func NewSSHClient(ctx context.Context, host *infrav1.LibvirtHost, secret *corev1.Secret) (*gossh.Client, error) {
	return NewSSHClientFactory(false)(ctx, host, secret)
}

// NewSSHClientFactory returns a client constructor bound to a host-key policy.
//
// allowInsecureHostKeys corresponds to the manager's --allow-insecure-host-keys
// flag. It defaults to false, in which case a LibvirtHost with no
// spec.hostKeyFingerprint is refused outright rather than connected to with host
// key verification silently disabled: an unauthenticated SSH channel hands the
// worker Ignition config — which carries the cluster's kubelet bootstrap
// credentials — to whoever answers on that address.
func NewSSHClientFactory(allowInsecureHostKeys bool) func(context.Context, *infrav1.LibvirtHost, *corev1.Secret) (*gossh.Client, error) {
	return func(ctx context.Context, host *infrav1.LibvirtHost, secret *corev1.Secret) (*gossh.Client, error) {
		return newSSHClient(ctx, host, secret, allowInsecureHostKeys)
	}
}

func newSSHClient(
	ctx context.Context,
	host *infrav1.LibvirtHost,
	secret *corev1.Secret,
	allowInsecureHostKeys bool,
) (*gossh.Client, error) {
	user, hostname, port, err := ParseLibvirtURI(host.Spec.URI)
	if err != nil {
		return nil, fmt.Errorf("parsing libvirt URI: %w", err)
	}

	hostKeyCallback, err := HostKeyCallback(host.Spec.HostKeyFingerprint, allowInsecureHostKeys)
	if err != nil {
		return nil, err
	}

	privateKeyBytes, ok := secret.Data["ssh-privatekey"]
	if !ok {
		return nil, fmt.Errorf("secret %s/%s missing 'ssh-privatekey' key", secret.Namespace, secret.Name)
	}

	signer, err := gossh.ParsePrivateKey(privateKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("parsing SSH private key: %w", err)
	}

	config := &gossh.ClientConfig{
		User: user,
		Auth: []gossh.AuthMethod{
			gossh.PublicKeys(signer),
		},
		HostKeyCallback: hostKeyCallback,
	}

	addr := net.JoinHostPort(hostname, strconv.Itoa(port))

	if host.Spec.HostKeyFingerprint == "" {
		return dial(ctx, addr, config)
	}
	var mismatches []error
	for _, algorithms := range hostKeyAlgorithmGroups {
		config.HostKeyAlgorithms = algorithms
		client, err := dial(ctx, addr, config)
		if err == nil {
			return client, nil
		}
		var negotiation *gossh.AlgorithmNegotiationError
		switch {
		case errors.Is(err, errHostKeyMismatch):
			mismatches = append(mismatches, err)
		case errors.As(err, &negotiation) && negotiation.What == "host key":
			// The host has no key of this type.
		default:
			return nil, err
		}
	}
	if len(mismatches) == 0 {
		return nil, fmt.Errorf("SSH handshake with %s: the host offered no host key of a supported type", addr)
	}
	return nil, errors.Join(mismatches...)
}

// dial opens an SSH connection to addr.
func dial(ctx context.Context, addr string, config *gossh.ClientConfig) (*gossh.Client, error) {
	// Use a dialer that respects context cancellation.
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dialing %s: %w", addr, err)
	}

	sshConn, chans, reqs, err := gossh.NewClientConn(conn, addr, config)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("SSH handshake with %s: %w", addr, err)
	}

	return gossh.NewClient(sshConn, chans, reqs), nil
}

// HostKeyCallback returns the SSH HostKeyCallback for a host, failing closed
// when no fingerprint is pinned.
//
// An empty expectedFingerprint is an error unless allowInsecure is set, because
// the alternative — gossh.InsecureIgnoreHostKey — accepts any host key and so
// accepts any impersonator. The fingerprint is emitted by
// deploy/ansible/setup-host.yaml at the end of host setup; copy it into
// LibvirtHost.spec.hostKeyFingerprint.
func HostKeyCallback(expectedFingerprint string, allowInsecure bool) (gossh.HostKeyCallback, error) {
	if expectedFingerprint == "" {
		if !allowInsecure {
			return nil, fmt.Errorf(
				"spec.hostKeyFingerprint is empty: refusing to connect without SSH host key verification " +
					"(set it to the host's SHA256:... fingerprint, or start the manager with " +
					"--allow-insecure-host-keys to accept any host key)")
		}
		return gossh.InsecureIgnoreHostKey(), nil
	}
	return verifyHostKey(expectedFingerprint), nil
}

// VerifyHostKey returns an SSH HostKeyCallback that verifies the remote host key
// fingerprint matches the expected SHA256 fingerprint. An empty
// expectedFingerprint rejects every host key; use HostKeyCallback to choose the
// policy for that case.
func VerifyHostKey(expectedFingerprint string) gossh.HostKeyCallback {
	return verifyHostKey(expectedFingerprint)
}

func verifyHostKey(expectedFingerprint string) gossh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key gossh.PublicKey) error {
		actual := gossh.FingerprintSHA256(key)
		if actual != expectedFingerprint {
			return fmt.Errorf("%w for %s: expected %s, got %s %s",
				errHostKeyMismatch, hostname, expectedFingerprint, key.Type(), actual)
		}
		return nil
	}
}

// ParseLibvirtURI parses a libvirt connection URI into its SSH components.
// Supported formats:
//   - qemu+ssh://user@host/system
//   - qemu+ssh://user@host:2222/system
//   - qemu+ssh://user@host/session
//
// The path (/system or /session) selects the daemon virsh connects to once
// on the host; see libvirt.LocalConnectionURI. Returns an error for non-SSH
// URIs (e.g. qemu:///system).
func ParseLibvirtURI(uri string) (user, host string, port int, err error) {
	if !strings.Contains(uri, "+ssh://") {
		return "", "", 0, fmt.Errorf("not an SSH URI: %s", uri)
	}

	parsed, err := url.Parse(uri)
	if err != nil {
		return "", "", 0, fmt.Errorf("invalid URI %q: %w", uri, err)
	}

	host = parsed.Hostname()
	if host == "" {
		return "", "", 0, fmt.Errorf("no host in URI %q", uri)
	}

	port = defaultSSHPort
	if parsed.Port() != "" {
		port, err = strconv.Atoi(parsed.Port())
		if err != nil {
			return "", "", 0, fmt.Errorf("invalid port in URI %q: %w", uri, err)
		}
	}

	user = "root" // default
	if parsed.User != nil && parsed.User.Username() != "" {
		user = parsed.User.Username()
	}

	return user, host, port, nil
}
