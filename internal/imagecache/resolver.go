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

// Package imagecache fetches large artifacts (currently qcow2 base images)
// from HTTPS / OCI / S3 sources and caches them in a content-addressed
// directory on the controller's local filesystem. Concurrent fetches for
// the same artifact coalesce via singleflight; the resolver layer only
// produces an io.ReadCloser, and the cache handles gzip decompression,
// sha256 verification, and atomic rename into the cache.
package imagecache

import (
	"context"
	"fmt"
	"io"
	"strings"

	infrav1 "github.com/atgreen/caplv/api/v1alpha1"
)

// Credentials carries the resolved pull secret for one Open call. nil means
// "anonymous". Fields are interpreted per transport, mirroring the
// bootartifacts package.
type Credentials struct {
	Username        string
	Password        string
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// Resolver opens a single-blob byte stream described by a BaseImageSource.
// Callers must Close the returned reader.
type Resolver interface {
	Open(ctx context.Context, src infrav1.BaseImageSource, creds *Credentials) (io.ReadCloser, error)
}

// MultiResolver dispatches Open to the transport-specific implementation.
type MultiResolver struct {
	HTTPS Resolver
	OCI   Resolver
	S3    Resolver

	// AllowInsecureTransport permits base image sources that disable transport
	// authentication: a plain http:// URL, oci.plainHTTP, s3.insecure, or any
	// of the insecureSkipTLSVerify flags. It corresponds to the manager's
	// --allow-insecure-image-transport flag and defaults to false.
	//
	// The bytes fetched here become the root disk of every worker VM in the
	// cluster, so an unauthenticated transport lets anyone on the network path
	// choose what those nodes boot. The sibling bootartifacts resolver has
	// always refused plain http for the same reason.
	AllowInsecureTransport bool
}

// NewMultiResolver returns a MultiResolver with the default implementations and
// insecure transports refused. Set AllowInsecureTransport to relax that.
func NewMultiResolver() *MultiResolver {
	return &MultiResolver{
		HTTPS: NewHTTPSResolver(),
		OCI:   NewOCIResolver(),
		S3:    NewS3Resolver(),
	}
}

// checkTransportPolicy rejects a source that would fetch the base image over a
// transport whose peer is not authenticated, unless the operator opted in.
func checkTransportPolicy(src infrav1.BaseImageSource, allowInsecure bool) error {
	if allowInsecure {
		return nil
	}
	const hint = "; fix the source, or start the manager with --allow-insecure-image-transport"
	switch src.Type {
	case infrav1.BootArtifactsSourceHTTPS:
		if src.HTTPS == nil {
			return nil
		}
		if strings.HasPrefix(strings.ToLower(src.HTTPS.URL), "http://") {
			return fmt.Errorf("baseImage.source.https.url uses plain http" + hint)
		}
		if src.HTTPS.InsecureSkipTLSVerify {
			return fmt.Errorf("baseImage.source.https.insecureSkipTLSVerify is set" + hint)
		}
	case infrav1.BootArtifactsSourceOCI:
		if src.OCI == nil {
			return nil
		}
		if src.OCI.PlainHTTP {
			return fmt.Errorf("baseImage.source.oci.plainHTTP is set" + hint)
		}
		if src.OCI.InsecureSkipTLSVerify {
			return fmt.Errorf("baseImage.source.oci.insecureSkipTLSVerify is set" + hint)
		}
	case infrav1.BootArtifactsSourceS3:
		if src.S3 == nil {
			return nil
		}
		if src.S3.Insecure {
			return fmt.Errorf("baseImage.source.s3.insecure is set" + hint)
		}
		if src.S3.InsecureSkipTLSVerify {
			return fmt.Errorf("baseImage.source.s3.insecureSkipTLSVerify is set" + hint)
		}
	}
	return nil
}

// Open dispatches to the configured per-type Resolver.
func (m *MultiResolver) Open(ctx context.Context, src infrav1.BaseImageSource, creds *Credentials) (io.ReadCloser, error) {
	if err := checkTransportPolicy(src, m.AllowInsecureTransport); err != nil {
		return nil, err
	}
	switch src.Type {
	case infrav1.BootArtifactsSourceHTTPS:
		if src.HTTPS == nil {
			return nil, fmt.Errorf("baseImage.source.https is required when type=HTTPS")
		}
		return m.HTTPS.Open(ctx, src, creds)
	case infrav1.BootArtifactsSourceOCI:
		if src.OCI == nil {
			return nil, fmt.Errorf("baseImage.source.oci is required when type=OCI")
		}
		return m.OCI.Open(ctx, src, creds)
	case infrav1.BootArtifactsSourceS3:
		if src.S3 == nil {
			return nil, fmt.Errorf("baseImage.source.s3 is required when type=S3")
		}
		return m.S3.Open(ctx, src, creds)
	default:
		return nil, fmt.Errorf("unsupported baseImage source type %q", src.Type)
	}
}
