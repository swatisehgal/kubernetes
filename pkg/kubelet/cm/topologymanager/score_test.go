/*
Copyright The Kubernetes Authors.

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

package topologymanager

import (
	"testing"

	utilfeature "k8s.io/apiserver/pkg/util/feature"
	featuregatetesting "k8s.io/component-base/featuregate/testing"
	"k8s.io/ktesting"
	pkgfeatures "k8s.io/kubernetes/pkg/features"
)

func TestUtilizationScore(t *testing.T) {
	tcases := []struct {
		name        string
		allocatable int64
		assigned    int64
		expected    int64
	}{
		{
			name:        "nothing allocatable reports no signal",
			allocatable: 0,
			assigned:    0,
			expected:    0,
		},
		{
			// Not reachable from the resource managers, but this guard is what
			// keeps the division safe, so pin both halves of it.
			name:        "negative allocatable reports no signal",
			allocatable: -1,
			assigned:    0,
			expected:    0,
		},
		{
			name:        "nothing assigned still scores 1",
			allocatable: 8,
			assigned:    0,
			expected:    1,
		},
		{
			name:        "half assigned",
			allocatable: 8,
			assigned:    4,
			expected:    50,
		},
		{
			name:        "fully assigned scores 100",
			allocatable: 8,
			assigned:    8,
			expected:    100,
		},
		{
			// 100 / 8 = 12.5, truncated to 12.
			name:        "fractional percentage truncates towards zero",
			allocatable: 8,
			assigned:    1,
			expected:    12,
		},
		{
			// 100 / 101 = 0.99, which truncates to 0. The floor keeps it a
			// signal: one CPU in use out of 101 is not the same as no signal.
			name:        "sub-one percent floors to 1",
			allocatable: 101,
			assigned:    1,
			expected:    1,
		},
		{
			// Assigning more than is allocatable means the caller's accounting
			// is wrong. The score is deliberately not clamped, so that surfaces
			// as an out-of-range value instead of being capped at a plausible
			// looking 100.
			name:        "over-assignment is not clamped",
			allocatable: 8,
			assigned:    9,
			expected:    112,
		},
	}

	for _, tc := range tcases {
		t.Run(tc.name, func(t *testing.T) {
			score := UtilizationScore(tc.allocatable, tc.assigned)
			if score != tc.expected {
				t.Errorf("UtilizationScore(%d, %d) = %d, expected %d", tc.allocatable, tc.assigned, score, tc.expected)
			}
		})
	}
}

// TestNewManagerSetsScoringEnabled checks that building a manager records
// whether hint providers need to compute a Score, which is the only thing
// that turns scoring on outside tests.
func TestNewManagerSetsScoringEnabled(t *testing.T) {
	logger, _ := ktesting.NewTestContext(t)

	tcases := []struct {
		description   string
		policyName    string
		policyOptions map[string]string
		alphaGate     bool
		expected      bool
		expectErr     bool
	}{
		{
			description: "the none policy never scores",
			policyName:  PolicyNone,
			alphaGate:   true,
			expected:    false,
		},
		{
			// NewManager is the only thing that turns scoring on, and it
			// cannot while the gate is off: NewPolicyOptions refuses the
			// option first. This is why the flag needs no gate check of its
			// own.
			description:   "the option is refused while the alpha gate is off",
			policyName:    PolicyBestEffort,
			policyOptions: map[string]string{NUMAAllocationStrategy: NUMAAllocationStrategyMostAllocated},
			alphaGate:     false,
			expected:      false,
			expectErr:     true,
		},
		{
			description: "no options at all while the alpha gate is off",
			policyName:  PolicyBestEffort,
			alphaGate:   false,
			expected:    false,
		},
		{
			description: "no allocation strategy means no scoring",
			policyName:  PolicyBestEffort,
			alphaGate:   true,
			expected:    false,
		},
		{
			description:   "an explicit none strategy means no scoring",
			policyName:    PolicyBestEffort,
			policyOptions: map[string]string{NUMAAllocationStrategy: NUMAAllocationStrategyNone},
			alphaGate:     true,
			expected:      false,
		},
		{
			description:   "most-allocated turns scoring on",
			policyName:    PolicyBestEffort,
			policyOptions: map[string]string{NUMAAllocationStrategy: NUMAAllocationStrategyMostAllocated},
			alphaGate:     true,
			expected:      true,
		},
		{
			description:   "least-allocated turns scoring on",
			policyName:    PolicySingleNumaNode,
			policyOptions: map[string]string{NUMAAllocationStrategy: NUMAAllocationStrategyLeastAllocated},
			alphaGate:     true,
			expected:      true,
		},
		{
			description:   "weights alone do not turn scoring on",
			policyName:    PolicyRestricted,
			policyOptions: map[string]string{NUMAScoreWeights: "cpu=5"},
			alphaGate:     true,
			expected:      false,
		},
	}

	for _, tc := range tcases {
		t.Run(tc.description, func(t *testing.T) {
			featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, pkgfeatures.TopologyManagerPolicyAlphaOptions, tc.alphaGate)
			// Start from the opposite value so the assertion cannot pass by
			// NewManager simply leaving the flag alone.
			defer SetScoringEnabled(!tc.expected)()

			_, err := NewManager(logger, nil, tc.policyName, ContainerTopologyScope, tc.policyOptions)
			if tc.expectErr {
				if err == nil {
					t.Fatalf("expected NewManager to fail")
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := ScoringEnabled(); got != tc.expected {
				t.Errorf("expected ScoringEnabled() to be %v, got %v", tc.expected, got)
			}
		})
	}
}
