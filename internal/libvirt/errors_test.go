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

package libvirt

import (
	"fmt"
	"testing"
)

func TestClassifyVirshError_NotFound(t *testing.T) {
	err := ClassifyVirshError("Domain not found", "lookup", "test-domain")
	if err.Category != ErrorNotFound {
		t.Errorf("expected ErrorNotFound, got %v", err.Category)
	}
}

func TestClassifyVirshError_AlreadyExists(t *testing.T) {
	err := ClassifyVirshError("Domain already exists", "define", "test-domain")
	if err.Category != ErrorTerminal {
		t.Errorf("expected ErrorTerminal for 'already exists', got %v", err.Category)
	}
}

func TestClassifyVirshError_PermissionDenied(t *testing.T) {
	err := ClassifyVirshError("permission denied", "start", "test-domain")
	if err.Category != ErrorTerminal {
		t.Errorf("expected ErrorTerminal for 'permission denied', got %v", err.Category)
	}
}

func TestClassifyVirshError_Transient(t *testing.T) {
	err := ClassifyVirshError("connection refused", "start", "test-domain")
	if err.Category != ErrorTransient {
		t.Errorf("expected ErrorTransient for unknown error, got %v", err.Category)
	}
}

func TestIsNotFound(t *testing.T) {
	notFoundErr := &LibvirtError{Category: ErrorNotFound, Op: "lookup", Resource: "dom", Err: fmt.Errorf("not found")}
	if !IsNotFound(notFoundErr) {
		t.Error("IsNotFound should return true for ErrorNotFound")
	}

	terminalErr := &LibvirtError{Category: ErrorTerminal, Op: "define", Resource: "dom", Err: fmt.Errorf("exists")}
	if IsNotFound(terminalErr) {
		t.Error("IsNotFound should return false for ErrorTerminal")
	}

	plainErr := fmt.Errorf("some other error")
	if IsNotFound(plainErr) {
		t.Error("IsNotFound should return false for non-LibvirtError")
	}
}

func TestIsTerminal(t *testing.T) {
	terminalErr := &LibvirtError{Category: ErrorTerminal, Op: "define", Resource: "dom", Err: fmt.Errorf("exists")}
	if !IsTerminal(terminalErr) {
		t.Error("IsTerminal should return true for ErrorTerminal")
	}

	notFoundErr := &LibvirtError{Category: ErrorNotFound, Op: "lookup", Resource: "dom", Err: fmt.Errorf("not found")}
	if IsTerminal(notFoundErr) {
		t.Error("IsTerminal should return false for ErrorNotFound")
	}

	plainErr := fmt.Errorf("some other error")
	if IsTerminal(plainErr) {
		t.Error("IsTerminal should return false for non-LibvirtError")
	}
}

func TestIsDomainNotRunning(t *testing.T) {
	// What virsh destroy prints for a domain that is shut off.
	shutOff := ClassifyVirshError("error: Failed to destroy domain 'w1'\n"+
		"error: Requested operation is not valid: domain is not running", "destroy", "w1")
	if !isDomainNotRunning(shutOff) {
		t.Error("isDomainNotRunning should return true for a shut-off domain")
	}
	if isDomainNotRunning(ClassifyVirshError("error: Failed to connect socket", "destroy", "w1")) {
		t.Error("isDomainNotRunning should return false for other errors")
	}
	if isDomainNotRunning(nil) {
		t.Error("isDomainNotRunning should return false for nil")
	}
}
