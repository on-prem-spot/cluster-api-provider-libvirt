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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func testTemplate() *infrav1.LibvirtMachineTemplate {
	return &infrav1.LibvirtMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "tmpl", Namespace: "default"},
		Spec: infrav1.LibvirtMachineTemplateSpec{
			Template: infrav1.LibvirtMachineTemplateResource{
				Spec: testMachineSpec(),
			},
		},
	}
}

func TestTemplateValidateCreate_Valid(t *testing.T) {
	v := &LibvirtMachineTemplateCustomValidator{}
	if _, err := v.ValidateCreate(context.Background(), testTemplate()); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

// A template must not be usable to smuggle in a spec the machine webhook
// would have rejected.
func TestTemplateValidateCreate_RejectsInvalidTemplatedSpec(t *testing.T) {
	v := &LibvirtMachineTemplateCustomValidator{}
	obj := testTemplate()
	// A key the machine webhook refuses: it would let a worker claim a role.
	obj.Spec.Template.Spec.NodeLabels = map[string]string{
		"node-role.kubernetes.io/master": "",
	}

	_, err := v.ValidateCreate(context.Background(), obj)
	if err == nil {
		t.Fatal("expected the restricted node label to be rejected")
	}
	if !strings.Contains(err.Error(), "LibvirtMachineTemplate") {
		t.Errorf("error should name the template kind, got: %v", err)
	}
}

// The reused machine validators report paths rooted at "spec", which does not
// exist on a template; the reader must be sent to the real field.
func TestTemplateValidationErrorsAreRerooted(t *testing.T) {
	v := &LibvirtMachineTemplateCustomValidator{}
	obj := testTemplate()
	obj.Spec.Template.Spec.NodeLabels = map[string]string{
		"node-role.kubernetes.io/master": "",
	}

	_, err := v.ValidateCreate(context.Background(), obj)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !apierrors.IsInvalid(err) {
		t.Fatalf("expected an Invalid error, got %T: %v", err, err)
	}
	statusErr, ok := err.(*apierrors.StatusError)
	if !ok || statusErr.ErrStatus.Details == nil {
		t.Fatalf("expected a StatusError carrying details, got %T: %v", err, err)
	}
	causes := statusErr.ErrStatus.Details.Causes
	if len(causes) == 0 {
		t.Fatal("expected at least one cause, otherwise this asserts nothing")
	}
	for _, cause := range causes {
		if !strings.HasPrefix(cause.Field, "spec.template.spec") {
			t.Errorf("field %q was not re-rooted under spec.template.spec", cause.Field)
		}
	}
}

// The collision is reported when the template is written, not when the second
// machine fails, but it cannot be an error: one replica is legitimate.
func TestTemplateValidateCreate_WarnsAboutScaling(t *testing.T) {
	v := &LibvirtMachineTemplateCustomValidator{}
	warnings, err := v.ValidateCreate(context.Background(), testTemplate())
	if err != nil {
		t.Fatalf("scaling limits must warn, not block: %v", err)
	}
	joined := strings.Join(warnings, " | ")
	if !strings.Contains(joined, "hostRef") {
		t.Errorf("expected a warning about the pinned host, got: %v", warnings)
	}
	if !strings.Contains(joined, "network.addresses") {
		t.Errorf("expected a warning about duplicate addresses, got: %v", warnings)
	}
}

// A template with neither a host nor static addresses has nothing to collide.
func TestTemplateValidateCreate_NoWarningsWhenNothingPinned(t *testing.T) {
	v := &LibvirtMachineTemplateCustomValidator{}
	obj := testTemplate()
	obj.Spec.Template.Spec.HostRef.Name = ""
	obj.Spec.Template.Spec.Network.Addresses = nil

	warnings, err := v.ValidateCreate(context.Background(), obj)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
}

// Editing a template in place would leave already-stamped machines silently
// not matching it, so the templated spec is immutable.
func TestTemplateValidateUpdate_SpecIsImmutable(t *testing.T) {
	v := &LibvirtMachineTemplateCustomValidator{}
	oldObj := testTemplate()
	newObj := testTemplate()
	newObj.Spec.Template.Spec.Domain.VCPUs = 8

	_, err := v.ValidateUpdate(context.Background(), oldObj, newObj)
	if err == nil {
		t.Fatal("expected a changed templated spec to be rejected")
	}
	if !strings.Contains(err.Error(), "immutable") {
		t.Errorf("error should explain immutability, got: %v", err)
	}
}

func TestTemplateValidateUpdate_UnchangedSpecAllowed(t *testing.T) {
	v := &LibvirtMachineTemplateCustomValidator{}
	oldObj := testTemplate()
	newObj := testTemplate()
	// Labels on the template object itself are not the templated spec.
	newObj.Labels = map[string]string{"example.com/owner": "platform"}

	if _, err := v.ValidateUpdate(context.Background(), oldObj, newObj); err != nil {
		t.Errorf("metadata-only change should be allowed, got: %v", err)
	}
}

func TestTemplateValidateDelete(t *testing.T) {
	v := &LibvirtMachineTemplateCustomValidator{}
	if _, err := v.ValidateDelete(context.Background(), testTemplate()); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}
