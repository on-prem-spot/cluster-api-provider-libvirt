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
	"fmt"

	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Event reasons. These are the stable, machine-readable half of an event and
// show up in `kubectl describe`, so they are treated as an interface: add
// freely, rename reluctantly.
const (
	// Machine domain lifecycle.
	EventDomainDefined   = "DomainDefined"
	EventDomainStarted   = "DomainStarted"
	EventDomainDestroyed = "DomainDestroyed"

	// Machine storage lifecycle.
	EventVolumeCreated       = "VolumeCreated"
	EventVolumeDeleted       = "VolumeDeleted"
	EventBaseImageUploaded   = "BaseImageUploaded"
	EventEphemeralPoolCreate = "EphemeralPoolCreated"
	EventEphemeralPoolDelete = "EphemeralPoolDestroyed"

	// Failures.
	EventProvisionFailed = "ProvisionFailed"
	EventDeleteFailed    = "DeleteFailed"

	// Cluster readiness.
	EventInfrastructureReady = "InfrastructureReady"
)

// recordEvent emits a Kubernetes event, tolerating a nil recorder.
//
// The nil check is what lets a reconciler be constructed without a Recorder:
// the unit tests do exactly that, and calling Event on a nil interface would
// panic rather than simply not record. A missing recorder should cost
// observability, never correctness.
func recordEvent(recorder record.EventRecorder, obj client.Object, eventType, reason, message string) {
	if recorder == nil || obj == nil {
		return
	}
	recorder.Event(obj, eventType, reason, message)
}

// recordEventf is recordEvent with formatting.
func recordEventf(
	recorder record.EventRecorder,
	obj client.Object,
	eventType, reason, format string,
	args ...any,
) {
	recordEvent(recorder, obj, eventType, reason, fmt.Sprintf(format, args...))
}
