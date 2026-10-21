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
	"errors"
	"fmt"
	"strings"
)

// ErrorCategory classifies libvirt errors for retry and error-handling logic.
type ErrorCategory int

const (
	// ErrorTerminal indicates an error that will not resolve by retrying.
	ErrorTerminal ErrorCategory = iota
	// ErrorTransient indicates an error that may resolve by retrying.
	ErrorTransient
	// ErrorNotFound indicates the requested resource does not exist.
	ErrorNotFound
)

// LibvirtError is a typed error for libvirt operations.
type LibvirtError struct {
	Category ErrorCategory
	Op       string
	Resource string
	Err      error
}

func (e *LibvirtError) Error() string {
	return fmt.Sprintf("libvirt %s %s: %v", e.Op, e.Resource, e.Err)
}

func (e *LibvirtError) Unwrap() error { return e.Err }

// IsNotFound returns true if the error indicates a resource was not found.
func IsNotFound(err error) bool {
	var le *LibvirtError
	if errors.As(err, &le) {
		return le.Category == ErrorNotFound
	}
	return false
}

// isDomainNotRunning reports whether err is libvirt refusing an operation
// because the domain is shut off, e.g. destroying a VM that something else
// already stopped (virsh destroy run on the host, or a guest that crashed).
func isDomainNotRunning(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "domain is not running")
}

// IsTerminal returns true if the error is terminal and should not be retried.
func IsTerminal(err error) bool {
	var le *LibvirtError
	if errors.As(err, &le) {
		return le.Category == ErrorTerminal
	}
	return false
}

// ClassifyVirshError parses virsh stderr output and returns a typed error.
func ClassifyVirshError(stderr, op, resource string) *LibvirtError {
	lower := strings.ToLower(stderr)

	category := ErrorTransient // default
	switch {
	case strings.Contains(lower, "not found"),
		strings.Contains(lower, "no domain"),
		strings.Contains(lower, "failed to get domain"),
		strings.Contains(lower, "no storage vol"),
		strings.Contains(lower, "storage volume not found"):
		category = ErrorNotFound
	case strings.Contains(lower, "already exists"):
		category = ErrorTerminal
	case strings.Contains(lower, "not authorized"),
		strings.Contains(lower, "permission denied"),
		strings.Contains(lower, "authentication failed"):
		category = ErrorTerminal
	}

	return &LibvirtError{
		Category: category,
		Op:       op,
		Resource: resource,
		Err:      fmt.Errorf("%s", stderr),
	}
}
