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

package e2enode

import (
	"fmt"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpuset"

	"k8s.io/kubernetes/test/e2e/framework"
)

type testCPUManagerEnvInfo struct {
	policy        string
	policyOptions []string
}

func checkSMTAlignment(f *framework.Framework, pod *v1.Pod, cnt *v1.Container, logs string, envInfo *testCPUManagerEnvInfo) error {
	var err error
	threadsPerCore := 1
	if isHTEnabled() {
		threadsPerCore = 2
	}

	podEnv, err := makeCPUAllowedListEnvMap(logs)
	if err != nil {
		return err
	}

	CPUIds, err := getCPUListAllowedFromEnv(f, pod, cnt, podEnv)
	if err != nil {
		return err
	}

	cpus := cpuset.NewCPUSet(CPUIds...)
	validateSMTAlignment(f, cpus, threadsPerCore, pod, cnt)
	return nil
}

func getCPUListAllowedFromEnv(f *framework.Framework, pod *v1.Pod, cnt *v1.Container, environ map[string]string) ([]int, error) {
	var cpuIDs []int
	cpuListAllowedEnvVar := "CPULIST_ALLOWED"

	for name, value := range environ {
		if name == cpuListAllowedEnvVar {
			cpus, err := cpuset.Parse(value)
			if err != nil {
				return nil, err
			}
			cpuIDs = cpus.ToSlice()
		}
	}
	if len(cpuIDs) == 0 {
		return nil, fmt.Errorf("variable %q not found in environ", cpuListAllowedEnvVar)
	}
	return cpuIDs, nil

}

func makeCPUAllowedListEnvMap(logs string) (map[string]string, error) {
	podEnv := strings.Split(logs, "\n")
	envMap := make(map[string]string)
	for _, envVar := range podEnv {
		if len(envVar) == 0 {
			continue
		}
		pair := strings.SplitN(envVar, "=", 2)
		if len(pair) != 2 {
			return nil, fmt.Errorf("unable to split %q", envVar)
		}
		envMap[pair[0]] = pair[1]
	}
	return envMap, nil
}

func validateSMTAlignment(f *framework.Framework, cpus cpuset.CPUSet, smtLevel int, pod *v1.Pod, cnt *v1.Container) {
	if cpus.Size()%smtLevel != 0 {
		framework.Failf("pod %q cnt %q received non-smt-multiple cpuset %v (SMT level %d)", pod.Name, cnt.Name, cpus, smtLevel)
	}
	// Here we check that all the given cpus are composed of thread siblings.
	// We rebuild the expected set of siblings from all the cpus we got and
	// if the expected set matches the given set, it would validate that all
	// the cpus allocated are smt-aligned.
	b := cpuset.NewBuilder()
	for _, cpuID := range cpus.ToSliceNoSort() {
		threadSiblings, err := cpuset.Parse(strings.TrimSpace(getCPUSiblingList(int64(cpuID))))
		framework.ExpectNoError(err, "parsing cpuset from logs for [%s] of pod [%s]", cnt.Name, pod.Name)
		b.Add(threadSiblings.ToSliceNoSort()...)
	}
	siblingsCPUs := b.Result()
	if !siblingsCPUs.Equals(cpus) {
		framework.Failf("pod %q cnt %q received non-smt-aligned cpuset %v (expected %v)", pod.Name, cnt.Name, cpus, siblingsCPUs)
	}
}
