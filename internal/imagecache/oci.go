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

package imagecache

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"

	infrav1 "github.com/atgreen/caplv/api/v1alpha1"
)

// maxManifestBytes caps an OCI manifest document, which is small JSON.
const maxManifestBytes = 4 << 20

// OCIResolver streams a qcow2 blob out of an OCI artifact. The manifest is
// expected to carry exactly one layer (or one layer titled BlobTitle when
// set), so packagers can do `oras push <ref> rhcos.qcow2:application/octet-stream`
// without any custom layout.
type OCIResolver struct{}

// NewOCIResolver returns an OCIResolver.
func NewOCIResolver() *OCIResolver { return &OCIResolver{} }

// Open fetches the manifest, selects the layer, and returns its blob stream.
func (r *OCIResolver) Open(ctx context.Context, src infrav1.BaseImageSource, creds *Credentials) (io.ReadCloser, error) {
	if src.OCI == nil {
		return nil, fmt.Errorf("oci source not set")
	}
	spec := src.OCI

	repo, err := remote.NewRepository(spec.Reference)
	if err != nil {
		return nil, fmt.Errorf("parse oci reference %q: %w", spec.Reference, err)
	}
	repo.PlainHTTP = spec.PlainHTTP

	client := &auth.Client{
		Client: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: spec.InsecureSkipTLSVerify, //nolint:gosec // user-opted-in for dev registries
				},
			},
		},
		Cache: auth.NewCache(),
	}
	client.Client.Transport = retry.NewTransport(client.Client.Transport)
	if creds != nil && (creds.Username != "" || creds.Password != "") {
		client.Credential = auth.StaticCredential(repo.Reference.Registry, auth.Credential{
			Username: creds.Username,
			Password: creds.Password,
		})
	}
	repo.Client = client

	tag := repo.Reference.Reference
	if tag == "" {
		return nil, fmt.Errorf("oci reference %q has no tag or digest", spec.Reference)
	}

	_, manifestBytes, err := fetchManifest(ctx, repo, tag)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest %s: %w", spec.Reference, err)
	}
	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("parse manifest %s: %w", spec.Reference, err)
	}

	layer, err := pickQcow2Layer(manifest.Layers, spec.BlobTitle)
	if err != nil {
		return nil, err
	}
	return repo.Fetch(ctx, layer)
}

func fetchManifest(ctx context.Context, repo *remote.Repository, ref string) (ocispec.Descriptor, []byte, error) {
	desc, rc, err := repo.Manifests().FetchReference(ctx, ref)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	defer func() { _ = rc.Close() }()
	// Bounded: the manifest is small JSON served by a remote registry.
	body, err := io.ReadAll(io.LimitReader(rc, maxManifestBytes+1))
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	if int64(len(body)) > maxManifestBytes {
		return ocispec.Descriptor{}, nil, fmt.Errorf("OCI manifest exceeds the %d byte limit", int64(maxManifestBytes))
	}
	return desc, body, nil
}

// pickQcow2Layer selects the qcow2 layer from a manifest. If title is set,
// returns the layer with that org.opencontainers.image.title annotation;
// otherwise returns the only layer in the manifest.
func pickQcow2Layer(layers []ocispec.Descriptor, title string) (ocispec.Descriptor, error) {
	if title == "" {
		if len(layers) != 1 {
			return ocispec.Descriptor{}, fmt.Errorf("manifest has %d layers; set baseImage.source.oci.blobTitle to disambiguate", len(layers))
		}
		return layers[0], nil
	}
	var match *ocispec.Descriptor
	for i := range layers {
		if layers[i].Annotations[ocispec.AnnotationTitle] == title {
			if match != nil {
				return ocispec.Descriptor{}, fmt.Errorf("ambiguous: multiple layers titled %q", title)
			}
			match = &layers[i]
		}
	}
	if match == nil {
		return ocispec.Descriptor{}, fmt.Errorf("no layer titled %q in manifest", title)
	}
	return *match, nil
}
