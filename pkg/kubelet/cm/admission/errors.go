/*
Copyright 2021 The Kubernetes Authors.

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

package admission

import (
	"fmt"

	"k8s.io/kubernetes/pkg/kubelet/cm/cpumanager/options"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
)

// TODO ugly name, but can't think of anything better
func DispatchError(err error) lifecycle.PodAdmitResult {
	if _, ok := err.(options.SMTAlignmentError); ok {
		return SMTAlignmentError(err)
	}
	return UnexpectedAdmissionError(err)
}

func TopologyAffinityError() lifecycle.PodAdmitResult {
	return lifecycle.PodAdmitResult{
		Message: "Resources cannot be allocated with Topology locality",
		Reason:  "TopologyAffinityError",
		Admit:   false,
	}
}

func UnexpectedAdmissionError(err error) lifecycle.PodAdmitResult {
	return lifecycle.PodAdmitResult{
		Message: fmt.Sprintf("Allocate failed due to %v, which is unexpected", err),
		Reason:  "UnexpectedAdmissionError",
		Admit:   false,
	}
}

func AdmitPod() lifecycle.PodAdmitResult {
	return lifecycle.PodAdmitResult{Admit: true}
}

func SMTAlignmentError(err error) lifecycle.PodAdmitResult {
	return lifecycle.PodAdmitResult{
		Message: err.Error(),
		Reason:  "SMTAlignmentError",
		Admit:   false,
	}
}
