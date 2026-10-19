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

// Package feature defines the CAPLV feature gates.
//
// A new, experimental behaviour is added behind a gate so it can ship
// disabled, be enabled per manager with --feature-gates=Name=true, and be
// promoted (alpha, default off -> beta, default on -> GA, gate removed) as it
// matures. Features that have already shipped ungated, such as session mode
// and ephemeral storage, are not retrofitted with gates: making them
// conditional now would only add a way to break existing users.
package feature

import (
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/component-base/featuregate"
)

// Every feature gate is declared in this file as a constant, following this
// template:
//
//	const (
//		// owner: @username
//		// alpha: v0.X
//		//
//		// MyFeature enables ...
//		MyFeature featuregate.Feature = "MyFeature"
//	)

func init() {
	runtime.Must(MutableGates.Add(defaultCAPLVFeatureGates))
}

// defaultCAPLVFeatureGates holds the default state of every CAPLV feature
// gate. To add a gate, declare its name above and add it here, e.g.
//
//	MyFeature: {Default: false, PreRelease: featuregate.Alpha},
var defaultCAPLVFeatureGates = map[featuregate.Feature]featuregate.FeatureSpec{}
