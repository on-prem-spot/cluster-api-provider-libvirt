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
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

const (
	defaultKernelLayerTitle    = "vmlinuz"
	defaultInitramfsLayerTitle = "initramfs.img"
)

// OCIResolver pulls kernel+initramfs from a single OCI artifact. Layers are
// selected by their `org.opencontainers.image.title` annotation (as set by
// `oras push`), so packagers can produce the artifact with one command:
//
//	oras push registry.example.com/caplv/boot:v1 \
//	    vmlinuz:application/octet-stream \
//	    initramfs.img:application/octet-stream
type OCIResolver struct {
	// Dir is where artifacts are stored.
	Dir string
}

// NewOCIResolver returns an OCIResolver that stores artifacts in dir. Per-call
// behavior (auth, TLS) is driven by the BootArtifactsSource and Credentials
// passed to Resolve.
func NewOCIResolver(dir string) *OCIResolver { return &OCIResolver{Dir: dir} }

// Resolve pulls the manifest at src.OCI.Reference, locates the kernel and
// initramfs layers by title annotation, and fetches their blobs.
func (r *OCIResolver) Resolve(ctx context.Context, src infrav1.BootArtifactsSource, creds *Credentials) (*Artifacts, error) {
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

	manifestDesc, manifestBytes, err := fetchManifest(ctx, repo, tag)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest %s: %w", spec.Reference, err)
	}
	_ = manifestDesc

	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("parse manifest %s: %w", spec.Reference, err)
	}

	kernelTitle := spec.KernelLayerTitle
	if kernelTitle == "" {
		kernelTitle = defaultKernelLayerTitle
	}
	initramfsTitle := spec.InitramfsLayerTitle
	if initramfsTitle == "" {
		initramfsTitle = defaultInitramfsLayerTitle
	}

	kernelLayer, err := pickLayerByTitle(manifest.Layers, kernelTitle)
	if err != nil {
		return nil, fmt.Errorf("kernel layer: %w", err)
	}
	initramfsLayer, err := pickLayerByTitle(manifest.Layers, initramfsTitle)
	if err != nil {
		return nil, fmt.Errorf("initramfs layer: %w", err)
	}

	kernelPath, kernelDigest, err := fetchLayer(ctx, repo, kernelLayer, spec.KernelSHA256, r.Dir)
	if err != nil {
		return nil, fmt.Errorf("kernel: %w", err)
	}
	initramfsPath, initramfsDigest, err := fetchLayer(ctx, repo, initramfsLayer, spec.InitramfsSHA256, r.Dir)
	if err != nil {
		return nil, fmt.Errorf("initramfs: %w", err)
	}

	return &Artifacts{
		KernelPath:      kernelPath,
		KernelSHA256:    kernelDigest,
		InitramfsPath:   initramfsPath,
		InitramfsSHA256: initramfsDigest,
	}, nil
}

func fetchManifest(ctx context.Context, repo *remote.Repository, ref string) (ocispec.Descriptor, []byte, error) {
	desc, rc, err := repo.Manifests().FetchReference(ctx, ref)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	defer func() { _ = rc.Close() }()
	body, err := readLimited(rc, maxManifestBytes, "OCI manifest")
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	return desc, body, nil
}

func pickLayerByTitle(layers []ocispec.Descriptor, title string) (ocispec.Descriptor, error) {
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

func fetchLayer(ctx context.Context, repo *remote.Repository, desc ocispec.Descriptor, expectedSHA256, dir string) (string, string, error) {
	rc, err := repo.Fetch(ctx, desc)
	if err != nil {
		return "", "", fmt.Errorf("fetch blob %s: %w", desc.Digest, err)
	}
	defer func() { _ = rc.Close() }()
	return storeArtifact(dir, rc, expectedSHA256, fmt.Sprintf("layer %s", desc.Digest))
}
