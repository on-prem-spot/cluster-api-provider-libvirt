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

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The expected shape: no endpoint, to be filled per cluster.
func testClusterTemplate() *infrav1.LibvirtClusterTemplate {
	return &infrav1.LibvirtClusterTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "ctmpl", Namespace: "default"},
		Spec: infrav1.LibvirtClusterTemplateSpec{
			Template: infrav1.LibvirtClusterTemplateResource{
				Spec: infrav1.LibvirtClusterSpec{},
			},
		},
	}
}

func TestClusterTemplateValidateCreate_NoEndpointIsCleanAndQuiet(t *testing.T) {
	v := &LibvirtClusterTemplateCustomValidator{}
	warnings, err := v.ValidateCreate(context.Background(), testClusterTemplate())
	if err != nil {
		t.Fatalf("a template without an endpoint is the intended shape: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
}

// Templating an endpoint copies one API address onto every cluster, which is
// worth saying out loud but is not always wrong.
func TestClusterTemplateValidateCreate_WarnsOnTemplatedEndpoint(t *testing.T) {
	v := &LibvirtClusterTemplateCustomValidator{}
	obj := testClusterTemplate()
	obj.Spec.Template.Spec.ControlPlaneEndpoint = infrav1.APIEndpoint{Host: "api.example.com", Port: 6443}

	warnings, err := v.ValidateCreate(context.Background(), obj)
	if err != nil {
		t.Fatalf("a templated endpoint must warn, not block: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected one warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "api.example.com") ||
		!strings.Contains(warnings[0], infrav1.ReasonWaitingForControlPlaneEndpoint) {
		t.Errorf("warning should name the endpoint and the waiting reason, got: %q", warnings[0])
	}
}

// A port with no host would be stamped onto every cluster and fail each of
// them the same way, so it is rejected rather than warned about.
func TestClusterTemplateValidateCreate_RejectsPortWithoutHost(t *testing.T) {
	v := &LibvirtClusterTemplateCustomValidator{}
	obj := testClusterTemplate()
	obj.Spec.Template.Spec.ControlPlaneEndpoint = infrav1.APIEndpoint{Port: 6443}

	_, err := v.ValidateCreate(context.Background(), obj)
	if err == nil {
		t.Fatal("expected a port without a host to be rejected")
	}
	if !strings.Contains(err.Error(), "set both or neither") {
		t.Errorf("error should explain the pairing, got: %v", err)
	}
}

func TestClusterTemplateValidateCreate_RejectsOutOfRangePort(t *testing.T) {
	v := &LibvirtClusterTemplateCustomValidator{}
	obj := testClusterTemplate()
	obj.Spec.Template.Spec.ControlPlaneEndpoint = infrav1.APIEndpoint{Host: "api.example.com", Port: 70000}

	_, err := v.ValidateCreate(context.Background(), obj)
	if err == nil {
		t.Fatal("expected an out-of-range port to be rejected")
	}
	if !strings.Contains(err.Error(), "65535") {
		t.Errorf("error should state the range, got: %v", err)
	}
}

func TestClusterTemplateValidateUpdate_SpecIsImmutable(t *testing.T) {
	v := &LibvirtClusterTemplateCustomValidator{}
	oldObj := testClusterTemplate()
	newObj := testClusterTemplate()
	newObj.Spec.Template.Spec.ControlPlaneEndpoint = infrav1.APIEndpoint{Host: "api.example.com", Port: 6443}

	_, err := v.ValidateUpdate(context.Background(), oldObj, newObj)
	if err == nil {
		t.Fatal("expected a changed templated spec to be rejected")
	}
	if !strings.Contains(err.Error(), "immutable") {
		t.Errorf("error should explain immutability, got: %v", err)
	}
}

func TestClusterTemplateValidateUpdate_UnchangedSpecAllowed(t *testing.T) {
	v := &LibvirtClusterTemplateCustomValidator{}
	oldObj := testClusterTemplate()
	newObj := testClusterTemplate()
	newObj.Labels = map[string]string{"example.com/owner": "platform"}

	if _, err := v.ValidateUpdate(context.Background(), oldObj, newObj); err != nil {
		t.Errorf("metadata-only change should be allowed, got: %v", err)
	}
}

func TestClusterTemplateValidateDelete(t *testing.T) {
	v := &LibvirtClusterTemplateCustomValidator{}
	if _, err := v.ValidateDelete(context.Background(), testClusterTemplate()); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}
