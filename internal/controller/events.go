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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
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

// Event actions. The events.k8s.io API separates the reason (why) from the
// action (what was being done), so these name the operation in progress.
const (
	ActionProvision   = "Provisioning"
	ActionDelete      = "Deleting"
	ActionHealthCheck = "HealthChecking"
)

// recordEvent emits a Kubernetes event, tolerating a nil recorder.
//
// The nil check is what lets a reconciler be constructed without a Recorder:
// the unit tests do exactly that, and calling Eventf on a nil interface would
// panic rather than simply not record. A missing recorder should cost
// observability, never correctness.
//
// related is always nil here: every event CAPLV emits concerns one object.
func recordEvent(
	recorder events.EventRecorder,
	obj runtime.Object,
	eventType, reason, action, note string,
	args ...any,
) {
	if recorder == nil || obj == nil {
		return
	}
	recorder.Eventf(obj, nil, eventType, reason, action, note, args...)
}
