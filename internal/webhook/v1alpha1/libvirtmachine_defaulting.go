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

	infrastructurev1alpha1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

// +kubebuilder:webhook:path=/mutate-infrastructure-cluster-x-k8s-io-v1alpha1-libvirtmachine,mutating=true,failurePolicy=fail,sideEffects=None,groups=infrastructure.cluster.x-k8s.io,resources=libvirtmachines,verbs=create;update,versions=v1alpha1,name=mlibvirtmachine-v1alpha1.kb.io,admissionReviewVersions=v1

// LibvirtMachineCustomDefaulter fills in values the CRD schema cannot express.
//
// Most of this type's defaults are plain schema defaults -- machine type,
// firmware, disk and NIC bus, clone strategy, bootstrap format -- and they
// belong there rather than here: the API server applies them without a webhook
// being reachable. What is left for a webhook is the cross-field case, where
// the default is another field's value and the schema has no way to say so.
//
// Deliberately not defaulted:
//
//   - domain.vcpus and domain.memoryMB. Zero is not "unset pending a default",
//     it is the documented request to size the VM from the host's spare
//     capacity. Writing a fixed number here would quietly disable that.
//   - rootDisk.ephemeralPoolSize. Empty means the size argument is omitted
//     from the tmpfs mount, so the kernel default (half of RAM) applies.
//     Deriving one from the disk size would change behaviour rather than
//     record it.
type LibvirtMachineCustomDefaulter struct{}

// Default implements the typed admission defaulter for LibvirtMachine.
func (d *LibvirtMachineCustomDefaulter) Default(
	_ context.Context,
	machine *infrastructurev1alpha1.LibvirtMachine,
) error {
	libvirtmachinelog.Info("Defaulting for LibvirtMachine", "name", machine.GetName())

	// An empty baseImagePool already means "the same pool as the root disk",
	// resolved independently by the controller and by the validating webhook.
	// Recording it makes the effective pool visible in the stored object,
	// so `kubectl get libvirtmachine -o yaml` answers which pool will be
	// read rather than leaving the reader to know the fallback.
	//
	// The readers keep their own fallbacks regardless: defaulting applies at
	// admission, so machines created before this webhook existed still carry
	// an empty value and must keep working.
	if machine.Spec.RootDisk.BaseImagePool == "" {
		machine.Spec.RootDisk.BaseImagePool = machine.Spec.RootDisk.StoragePool
	}

	return nil
}
