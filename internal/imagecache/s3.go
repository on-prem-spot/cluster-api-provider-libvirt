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
	"fmt"
	"io"
	"net/http"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

// S3Resolver streams a qcow2 from an S3-compatible bucket.
type S3Resolver struct{}

// NewS3Resolver returns an S3Resolver.
func NewS3Resolver() *S3Resolver { return &S3Resolver{} }

// Open issues GetObject and returns its body.
func (r *S3Resolver) Open(ctx context.Context, src infrav1.BaseImageSource, creds *Credentials) (io.ReadCloser, error) {
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

	client, err := minio.New(spec.Endpoint, &minio.Options{
		Creds:        creds4,
		Secure:       !spec.Insecure,
		Region:       spec.Region,
		BucketLookup: lookupStyle(spec.UsePathStyle),
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: spec.InsecureSkipTLSVerify, //nolint:gosec // user-opted-in for dev endpoints
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("init s3 client for %s: %w", spec.Endpoint, err)
	}

	obj, err := client.GetObject(ctx, spec.Bucket, spec.Key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("get s3://%s/%s: %w", spec.Bucket, spec.Key, err)
	}
	return obj, nil
}

func lookupStyle(usePath bool) minio.BucketLookupType {
	if usePath {
		return minio.BucketLookupPath
	}
	return minio.BucketLookupAuto
}
