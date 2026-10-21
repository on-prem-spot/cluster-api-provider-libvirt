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
	"fmt"
	"net/http"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

// S3Resolver pulls kernel+initramfs from an S3-compatible object store via
// the minio-go client (works with AWS S3, MinIO, Ceph RGW, GCS XML API).
type S3Resolver struct {
	// Dir is where artifacts are stored.
	Dir string
}

// NewS3Resolver returns an S3Resolver that stores artifacts in dir. Per-call
// behavior (endpoint, TLS, addressing style, credentials) is driven by the
// BootArtifactsSource and Credentials passed to Resolve.
func NewS3Resolver(dir string) *S3Resolver { return &S3Resolver{Dir: dir} }

// Resolve fetches both keys from the configured bucket, verifies any
// user-supplied digests, and returns the artifact bytes.
func (r *S3Resolver) Resolve(ctx context.Context, src infrav1.BootArtifactsSource, creds *Credentials) (*Artifacts, error) {
	if src.S3 == nil {
		return nil, fmt.Errorf("s3 source not set")
	}
	spec := src.S3

	var creds4 *credentials.Credentials
	if creds != nil && creds.AccessKeyID != "" {
		creds4 = credentials.NewStaticV4(creds.AccessKeyID, creds.SecretAccessKey, creds.SessionToken)
	} else {
		creds4 = credentials.NewStaticV4("", "", "")
	}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: spec.InsecureSkipTLSVerify, //nolint:gosec // user-opted-in for dev endpoints
		},
	}

	client, err := minio.New(spec.Endpoint, &minio.Options{
		Creds:        creds4,
		Secure:       !spec.Insecure,
		Region:       spec.Region,
		BucketLookup: lookupStyle(spec.UsePathStyle),
		Transport:    transport,
	})
	if err != nil {
		return nil, fmt.Errorf("init s3 client for %s: %w", spec.Endpoint, err)
	}

	kernelPath, kernelDigest, err := fetchObject(ctx, client, spec.Bucket, spec.KernelKey, spec.KernelSHA256, r.Dir)
	if err != nil {
		return nil, fmt.Errorf("kernel: %w", err)
	}
	initramfsPath, initramfsDigest, err := fetchObject(ctx, client, spec.Bucket, spec.InitramfsKey, spec.InitramfsSHA256, r.Dir)
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

func lookupStyle(usePath bool) minio.BucketLookupType {
	if usePath {
		return minio.BucketLookupPath
	}
	return minio.BucketLookupAuto
}

func fetchObject(ctx context.Context, client *minio.Client, bucket, key, expectedSHA256, dir string) (string, string, error) {
	obj, err := client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return "", "", fmt.Errorf("get s3://%s/%s: %w", bucket, key, err)
	}
	defer func() { _ = obj.Close() }()
	return storeArtifact(dir, obj, expectedSHA256, fmt.Sprintf("s3://%s/%s", bucket, key))
}
