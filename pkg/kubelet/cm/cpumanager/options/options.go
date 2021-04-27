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

package options

import (
	"fmt"
)

const (
	// SMTAware is the name of the CPU Manager policy option
	SMTAware string = "smtaware"
)

func IsSMTAwareEnabled(policyOptions []string) bool {
	for _, option := range policyOptions {
		if option == SMTAware {
			return true
		}
	}
	return false
}

type SMTAlignmentError struct {
	RequestedCPUs int
	CpusPerCore   int
}

func (e SMTAlignmentError) Error() string {
	return fmt.Sprintf("Number of CPUs requested should be a multiple of number of CPUs on a core = %d on this system. Requested CPU count = %d", e.CpusPerCore, e.RequestedCPUs)
}
