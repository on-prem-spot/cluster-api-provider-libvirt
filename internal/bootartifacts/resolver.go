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

package bootartifacts

import (
	"context"
	"fmt"
	"io"

	infrav1 "github.com/atgreen/caplv/api/v1alpha1"
)

const (
	// MaxArtifactBytes caps a single kernel or initramfs, before and after gzip
	// decompression. These artifacts are held wholly in memory and then cached
	// for the process lifetime, so an unbounded read is an OOM of the manager —
	// and since the digest can only be checked once the bytes are in hand, a
	// pinned sha256 is no defence against a decompression bomb. 512 MiB is well
	// clear of a real RHCOS kernel+initramfs pair.
	MaxArtifactBytes = 512 << 20
	// maxManifestBytes caps an OCI manifest document, which is small JSON.
	maxManifestBytes = 4 << 20
)

// readLimited reads at most limit bytes from r, returning an error rather than
// a truncated result when the source has more to give.
func readLimited(r io.Reader, limit int64, what string) ([]byte, error) {
	buf, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(buf)) > limit {
		return nil, fmt.Errorf("%s exceeds the %d byte limit", what, limit)
	}
	return buf, nil
}

// Artifacts holds the fetched kernel and initramfs bytes plus their sha256 hex digests.
type Artifacts struct {
	KernelBytes     []byte
	KernelSHA256    string
	InitramfsBytes  []byte
	InitramfsSHA256 string
}

// Credentials carries the resolved pull secrets for one Resolve call. The
// controller is responsible for reading the referenced Kubernetes Secret and
// constructing this struct; resolvers never touch the kube client.
//
// A nil Credentials value means "anonymous". Fields are interpreted per
// transport:
//   - OCI: Username/Password (registry basic auth or token).
//   - S3:  AccessKeyID/SecretAccessKey/SessionToken.
//   - HTTPS: not used today (digests are sufficient).
type Credentials struct {
	Username        string
	Password        string
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// Resolver fetches Artifacts described by a BootArtifactsSource.
type Resolver interface {
	Resolve(ctx context.Context, src infrav1.BootArtifactsSource, creds *Credentials) (*Artifacts, error)
}

// MultiResolver dispatches Resolve to the transport-specific implementation
// indicated by src.Type.
type MultiResolver struct {
	HTTPS Resolver
	OCI   Resolver
	S3    Resolver

	// AllowInsecureTransport permits sources that disable transport
	// authentication (oci.plainHTTP, s3.insecure, or either
	// insecureSkipTLSVerify). It corresponds to the manager's
	// --allow-insecure-image-transport flag and defaults to false.
	//
	// The HTTPS resolver has always rejected plain http because these bytes are
	// the kernel and initramfs every VM boots; the same reasoning applies to the
	// registry and object-store transports, which previously honoured their
	// insecure knobs unconditionally.
	AllowInsecureTransport bool
}

// checkTransportPolicy rejects a source whose transport leaves the peer
// unauthenticated, unless the operator opted in.
func checkTransportPolicy(src infrav1.BootArtifactsSource, allowInsecure bool) error {
	if allowInsecure {
		return nil
	}
	const hint = "; fix the source, or start the manager with --allow-insecure-image-transport"
	switch src.Type {
	case infrav1.BootArtifactsSourceOCI:
		if src.OCI == nil {
			return nil
		}
		if src.OCI.PlainHTTP {
			return fmt.Errorf("bootArtifacts.source.oci.plainHTTP is set" + hint)
		}
		if src.OCI.InsecureSkipTLSVerify {
			return fmt.Errorf("bootArtifacts.source.oci.insecureSkipTLSVerify is set" + hint)
		}
	case infrav1.BootArtifactsSourceS3:
		if src.S3 == nil {
			return nil
		}
		if src.S3.Insecure {
			return fmt.Errorf("bootArtifacts.source.s3.insecure is set" + hint)
		}
		if src.S3.InsecureSkipTLSVerify {
			return fmt.Errorf("bootArtifacts.source.s3.insecureSkipTLSVerify is set" + hint)
		}
	}
	return nil
}

// NewMultiResolver returns a MultiResolver wired with the default
// implementations for each supported transport.
func NewMultiResolver() *MultiResolver {
	return &MultiResolver{
		HTTPS: NewHTTPSResolver(),
		OCI:   NewOCIResolver(),
		S3:    NewS3Resolver(),
	}
}

// Resolve dispatches to the configured per-type Resolver.
func (m *MultiResolver) Resolve(ctx context.Context, src infrav1.BootArtifactsSource, creds *Credentials) (*Artifacts, error) {
	if err := checkTransportPolicy(src, m.AllowInsecureTransport); err != nil {
		return nil, err
	}
	switch src.Type {
	case infrav1.BootArtifactsSourceHTTPS:
		if src.HTTPS == nil {
			return nil, fmt.Errorf("bootArtifacts.source.https is required when type=HTTPS")
		}
		return m.HTTPS.Resolve(ctx, src, creds)
	case infrav1.BootArtifactsSourceOCI:
		if src.OCI == nil {
			return nil, fmt.Errorf("bootArtifacts.source.oci is required when type=OCI")
		}
		return m.OCI.Resolve(ctx, src, creds)
	case infrav1.BootArtifactsSourceS3:
		if src.S3 == nil {
			return nil, fmt.Errorf("bootArtifacts.source.s3 is required when type=S3")
		}
		return m.S3.Resolve(ctx, src, creds)
	default:
		return nil, fmt.Errorf("unsupported bootArtifacts source type %q", src.Type)
	}
}
