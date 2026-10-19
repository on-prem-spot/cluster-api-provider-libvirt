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
	"testing"

	"github.com/spf13/pflag"
	"k8s.io/component-base/featuregate"
)

func TestFeatureGateFlagParses(t *testing.T) {
	gates := featuregate.NewFeatureGate()
	if err := gates.Add(map[featuregate.Feature]featuregate.FeatureSpec{
		"TestFeature": {Default: false, PreRelease: featuregate.Alpha},
	}); err != nil {
		t.Fatal(err)
	}
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	gates.AddFlag(fs)
	if err := fs.Parse([]string{"--feature-gates=TestFeature=true"}); err != nil {
		t.Fatal(err)
	}
	if !gates.Enabled("TestFeature") {
		t.Error("TestFeature not enabled by --feature-gates")
	}
}

func TestDefaultGatesRegister(t *testing.T) {
	for name, spec := range defaultCAPLVFeatureGates {
		if Gates.Enabled(name) != spec.Default {
			t.Errorf("%s: enabled=%v, want default %v", name, Gates.Enabled(name), spec.Default)
		}
	}
}
