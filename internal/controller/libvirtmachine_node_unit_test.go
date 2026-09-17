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

package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func nodeWithProviderID(id string) *corev1.Node {
	return &corev1.Node{Spec: corev1.NodeSpec{ProviderID: id}}
}

func TestNodeBelongsToMachine_OwnNode(t *testing.T) {
	id := "libvirt:///host-a/caplv-prod-worker-1"
	owned, known := nodeBelongsToMachine(nodeWithProviderID(id), id)
	if !known {
		t.Fatal("providerID is set, so ownership should be decidable")
	}
	if !owned {
		t.Error("a Node carrying this machine's providerID should be owned by it")
	}
}

// Two machines can produce the same artifact base name, because
// ArtifactBaseName joins namespace/cluster/machine with an unescaped '-'. The
// Node must therefore be matched on providerID, not on the name that led us to
// it — otherwise one machine can relabel or delete another's Node.
func TestNodeBelongsToMachine_RejectsCollidingMachine(t *testing.T) {
	victim := "libvirt:///host-a/caplv-prod-worker-1"
	attacker := "libvirt:///host-b/caplv-prod-worker-1"
	owned, known := nodeBelongsToMachine(nodeWithProviderID(victim), attacker)
	if !known {
		t.Fatal("providerID is set, so ownership should be decidable")
	}
	if owned {
		t.Error("a Node belonging to another machine must not be claimed")
	}
}

func TestNodeBelongsToMachine_UnknownBeforeProviderIDReported(t *testing.T) {
	owned, known := nodeBelongsToMachine(nodeWithProviderID(""), "libvirt:///host-a/n")
	if known {
		t.Error("a Node with no providerID yet is undecided, not a mismatch")
	}
	if owned {
		t.Error("an undecided Node must not be treated as owned")
	}
}
