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

package cpumanager

import (
	"reflect"
	"testing"
)

type optionTest struct {
	description   string
	policyOptions []string
	expectedError bool
	expectedValue Options
}

func TestOptions(t *testing.T) {
	testCases := []optionTest{
		{
			description:   "nil args",
			policyOptions: nil,
			expectedError: false,
			expectedValue: Options{},
		},
		{
			description:   "empty args",
			policyOptions: []string{},
			expectedError: false,
			expectedValue: Options{},
		},
		{
			description:   "bad single arg",
			policyOptions: []string{"badValue1"},
			expectedError: true,
		},
		{
			description:   "bad multiple arg",
			policyOptions: []string{"badValue1", "badvalue2"},
			expectedError: true,
		},
		{
			description:   "good arg",
			policyOptions: []string{SMTAlignPolicyOption},
			expectedError: false,
			expectedValue: Options{
				SMTAlignmentEnabled: true,
			},
		},
		{
			description:   "bad arg intermixed",
			policyOptions: []string{SMTAlignPolicyOption, "badvalue2"},
			expectedError: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.description, func(t *testing.T) {
			opts, err := NewOptions(testCase.policyOptions)
			gotError := (err != nil)
			if gotError != testCase.expectedError {
				t.Fatalf("error with args %v expected error %v got %v: %v",
					testCase.policyOptions, testCase.expectedError, gotError, err)
			}

			if testCase.expectedError {
				return
			}

			if !reflect.DeepEqual(opts, testCase.expectedValue) {
				t.Fatalf("value mismatch with args %v expected value %v got %v",
					testCase.policyOptions, testCase.expectedValue, opts)
			}
		})
	}

}
