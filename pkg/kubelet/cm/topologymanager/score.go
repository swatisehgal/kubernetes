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

import "sync/atomic"

// scoringEnabled records whether the configured policy reads hint Scores. The
// kubelet builds one topology manager, before any hint provider runs, so
// recording the answer here is enough and saves threading it through every
// provider.
var scoringEnabled atomic.Bool

// ScoringEnabled reports whether hint providers need to compute a Score.
//
// Scores are read only by a numa-allocation-strategy, which is off by
// default, and computing one costs real work: the device manager walks every
// device of a resource once per candidate NUMA mask, and the mask count grows
// as 2^numaNodes. A provider that checks this first skips that work entirely
// on a default node.
//
// Skipping leaves the hint at Score 0, which the merge already treats as "no
// signal" and excludes from aggregation, so hint selection is unchanged.
func ScoringEnabled() bool {
	return scoringEnabled.Load()
}

// SetScoringEnabled forces whether hint providers compute a Score, and returns
// a function restoring the previous setting.
//
// Only hint provider tests need this: they build providers directly rather
// than through NewManager, which is what normally decides.
func SetScoringEnabled(enabled bool) func() {
	previous := scoringEnabled.Swap(enabled)
	return func() { scoringEnabled.Store(previous) }
}

// UtilizationScore turns a pair of resource counts into the Score of a
// TopologyHint: the share of the allocatable resources on the hint's NUMA
// node(s) that is already assigned, as a percentage.
//
// It returns 0 when nothing is allocatable. 0 means "no signal" rather than
// "empty", which is why anything with allocatable resources scores at least 1
// however little of it is in use: the hint merge skips unscored contributors
// instead of averaging them in, so 0 and 1 are not neighbouring values.
//
// The resource managers share this so that CPU, memory and device scores stay
// comparable — the merge averages them with equal weight — and so the floor and
// the divide-by-zero guard cannot drift apart.
func UtilizationScore(allocatable, assigned int64) int64 {
	if allocatable <= 0 {
		return 0
	}
	return max(1, assigned*100/allocatable)
}
