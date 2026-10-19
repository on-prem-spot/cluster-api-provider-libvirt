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

package feature

import (
	"k8s.io/component-base/featuregate"
)

var (
	// MutableGates is a mutable version of Gates. Only top-level commands,
	// such as cmd/main.go, should use it to register the --feature-gates
	// flag; everything else reads Gates.
	MutableGates featuregate.MutableFeatureGate = featuregate.NewFeatureGate()

	// Gates is the read-only view of the CAPLV feature gates. Check a gate
	// with feature.Gates.Enabled(feature.MyFeature).
	Gates featuregate.FeatureGate = MutableGates
)
