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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	conditions "sigs.k8s.io/cluster-api/util/conditions/v1beta2"
	"testing"
)

func TestCAPIConditions(t *testing.T) {
	for name, obj := range map[string]conditions.Setter{
		"cluster": &LibvirtCluster{}, "machine": &LibvirtMachine{}, "host": &LibvirtHost{},
	} {
		t.Run(name, func(t *testing.T) {
			conditions.Set(obj, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Provisioned", Message: "Ready"})
			if !meta.IsStatusConditionTrue(obj.GetV1Beta2Conditions(), "Ready") {
				t.Fatal("CAPI condition was not stored")
			}
			conditions.Set(obj, metav1.Condition{Type: "Ready", Status: metav1.ConditionFalse, Reason: "Failed", Message: "Failed"})
			if len(obj.GetV1Beta2Conditions()) != 1 || conditions.Get(obj, "Ready").Status != metav1.ConditionFalse {
				t.Fatal("CAPI condition was not updated")
			}
		})
	}
}
