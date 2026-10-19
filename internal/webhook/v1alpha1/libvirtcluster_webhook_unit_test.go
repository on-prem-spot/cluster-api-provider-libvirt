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

package v1alpha1

import (
	"context"
	"strings"
	"testing"

	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

const testDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func httpsBootArtifacts() *infrav1.BootArtifactsSpec {
	return &infrav1.BootArtifactsSpec{
		HostPath: "/var/lib/caplv/boot",
		Source: infrav1.BootArtifactsSource{
			Type: infrav1.BootArtifactsSourceHTTPS,
			HTTPS: &infrav1.HTTPSBootArtifactsSource{
				KernelURL:    "https://mirror.example.com/vmlinuz",
				InitramfsURL: "https://mirror.example.com/initramfs.img",
				KernelSHA256: testDigest,
			},
		},
	}
}

func TestLibvirtClusterValidateCreate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*infrav1.LibvirtClusterSpec)
		wantErr string
	}{
		{name: "empty spec"},
		{name: "endpoint set", mutate: func(s *infrav1.LibvirtClusterSpec) {
			s.ControlPlaneEndpoint = clusterv1.APIEndpoint{Host: "api.example.com", Port: 6443}
		}},
		{name: "port out of range", wantErr: "spec.controlPlaneEndpoint.port", mutate: func(s *infrav1.LibvirtClusterSpec) {
			s.ControlPlaneEndpoint = clusterv1.APIEndpoint{Host: "api.example.com", Port: 70000}
		}},
		{name: "port without host", wantErr: "spec.controlPlaneEndpoint", mutate: func(s *infrav1.LibvirtClusterSpec) {
			s.ControlPlaneEndpoint.Port = 6443
		}},
		{name: "valid https boot artifacts", mutate: func(s *infrav1.LibvirtClusterSpec) {
			s.BootArtifacts = httpsBootArtifacts()
		}},
		{name: "boot artifacts over plain http", wantErr: "spec.bootArtifacts.source.https.kernelURL", mutate: func(s *infrav1.LibvirtClusterSpec) {
			s.BootArtifacts = httpsBootArtifacts()
			s.BootArtifacts.Source.HTTPS.KernelURL = "http://mirror.example.com/vmlinuz"
		}},
		{name: "relative boot artifact URL", wantErr: "spec.bootArtifacts.source.https.initramfsURL", mutate: func(s *infrav1.LibvirtClusterSpec) {
			s.BootArtifacts = httpsBootArtifacts()
			s.BootArtifacts.Source.HTTPS.InitramfsURL = "initramfs.img"
		}},
		{name: "prefixed digest", wantErr: "spec.bootArtifacts.source.https.kernelSHA256", mutate: func(s *infrav1.LibvirtClusterSpec) {
			s.BootArtifacts = httpsBootArtifacts()
			s.BootArtifacts.Source.HTTPS.KernelSHA256 = "sha256:" + testDigest
		}},
		{name: "member missing for type", wantErr: "spec.bootArtifacts.source.oci", mutate: func(s *infrav1.LibvirtClusterSpec) {
			s.BootArtifacts = httpsBootArtifacts()
			s.BootArtifacts.Source.Type = infrav1.BootArtifactsSourceOCI
		}},
		{name: "base image over http is left to the controller", mutate: func(s *infrav1.LibvirtClusterSpec) {
			s.BaseImage = &infrav1.BaseImageSpec{Pool: "default", VolumeName: "rhcos.qcow2", Source: infrav1.BaseImageSource{
				Type: infrav1.BootArtifactsSourceHTTPS, HTTPS: &infrav1.HTTPSBaseImageSource{URL: "http://mirror.example.com/rhcos.qcow2"}}}
		}},
		{name: "base image with a second member", wantErr: "spec.baseImage.source.s3", mutate: func(s *infrav1.LibvirtClusterSpec) {
			s.BaseImage = &infrav1.BaseImageSpec{Pool: "default", VolumeName: "rhcos.qcow2", Source: infrav1.BaseImageSource{
				Type:  infrav1.BootArtifactsSourceHTTPS,
				HTTPS: &infrav1.HTTPSBaseImageSource{URL: "https://mirror.example.com/rhcos.qcow2"},
				S3:    &infrav1.S3BaseImageSource{Endpoint: "s3.example.com", Bucket: "b", Key: "k"}}}
		}},
		{name: "S3 endpoint with scheme", wantErr: "spec.baseImage.source.s3.endpoint", mutate: func(s *infrav1.LibvirtClusterSpec) {
			s.BaseImage = &infrav1.BaseImageSpec{Pool: "default", VolumeName: "rhcos.qcow2", Source: infrav1.BaseImageSource{
				Type: infrav1.BootArtifactsSourceS3,
				S3:   &infrav1.S3BaseImageSource{Endpoint: "https://s3.example.com", Bucket: "b", Key: "k"}}}
		}},
		{name: "OCI reference as URL", wantErr: "spec.baseImage.source.oci.reference", mutate: func(s *infrav1.LibvirtClusterSpec) {
			s.BaseImage = &infrav1.BaseImageSpec{Pool: "default", VolumeName: "rhcos.qcow2", Source: infrav1.BaseImageSource{
				Type: infrav1.BootArtifactsSourceOCI,
				OCI:  &infrav1.OCIBaseImageSource{Reference: "https://quay.io/org/rhcos:4.16"}}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obj := &infrav1.LibvirtCluster{}
			if tc.mutate != nil {
				tc.mutate(&obj.Spec)
			}
			_, err := (&LibvirtClusterCustomValidator{}).ValidateCreate(context.Background(), obj)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("expected an error on %s, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestLibvirtClusterEndpointImmutableOnceSet(t *testing.T) {
	v := &LibvirtClusterCustomValidator{}
	unset := &infrav1.LibvirtCluster{}
	set := &infrav1.LibvirtCluster{Spec: infrav1.LibvirtClusterSpec{
		ControlPlaneEndpoint: clusterv1.APIEndpoint{Host: "api.example.com", Port: 6443}}}
	moved := set.DeepCopy()
	moved.Spec.ControlPlaneEndpoint.Host = "api2.example.com"

	if _, err := v.ValidateUpdate(context.Background(), unset, set); err != nil {
		t.Errorf("setting the endpoint was rejected: %v", err)
	}
	if _, err := v.ValidateUpdate(context.Background(), set, moved); err == nil {
		t.Error("changing a set endpoint was accepted")
	}
	withImage := set.DeepCopy()
	withImage.Spec.BootArtifacts = httpsBootArtifacts()
	if _, err := v.ValidateUpdate(context.Background(), set, withImage); err != nil {
		t.Errorf("changing boot artifacts was rejected: %v", err)
	}
}

func TestLibvirtClusterWarnsOnInsecureTransport(t *testing.T) {
	obj := &infrav1.LibvirtCluster{Spec: infrav1.LibvirtClusterSpec{BaseImage: &infrav1.BaseImageSpec{
		Pool: "default", VolumeName: "rhcos.qcow2", Source: infrav1.BaseImageSource{
			Type: infrav1.BootArtifactsSourceOCI,
			OCI:  &infrav1.OCIBaseImageSource{Reference: "registry.local/rhcos:4.16", PlainHTTP: true}}}}}

	warnings, err := (&LibvirtClusterCustomValidator{}).ValidateCreate(context.Background(), obj)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "oci.plainHTTP") {
		t.Errorf("expected a plainHTTP warning, got %v", warnings)
	}
	warnings, _ = (&LibvirtClusterCustomValidator{AllowInsecureImageTransport: true}).ValidateCreate(context.Background(), obj)
	if len(warnings) != 0 {
		t.Errorf("expected no warning with insecure transport allowed, got %v", warnings)
	}
}
