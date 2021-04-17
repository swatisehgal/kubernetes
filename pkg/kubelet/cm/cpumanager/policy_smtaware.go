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
	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpumanager/topology"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpuset"
	"k8s.io/kubernetes/pkg/kubelet/cm/topologymanager"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
)

// PolicySMTAware is the name of the smtaware policy
const PolicySMTAware policyName = "smtaware"

// smtAwarePolicy is a CPU manager policy that is a further refinement of the existing static policy.
// This policy allocates CPUs exclusively for a container if all the following
// conditions are met:
//
// - The pod QoS class is Guaranteed.
// - The CPU request is a positive even integer.
// This policy ensures that containers will be admitted by the kubelet only if the CPU request is even integer.
// In case CPU request is not even, the pod will not be admitted and will fail with `SMTAlignmentError`.
// The rational behind this is to ensure that in case of SMT-enabled systems, a guaranteed container never
// shares a physical core with other containers.
type smtAwarePolicy struct {
	*staticPolicy
}

// Ensure smtAwarePolicy implements Policy interface
var _ Policy = &smtAwarePolicy{}

// NewsmtAwarePolicy returns a CPU manager policy that does not change CPU
// assignments for exclusively pinned guaranteed containers after the main
// container process starts.
func NewSMTAwarePolicy(topology *topology.CPUTopology, numReservedCPUs int, reservedCPUs cpuset.CPUSet, affinity topologymanager.Store) (Policy, error) {
	pol, err := NewStaticPolicy(topology, numReservedCPUs, reservedCPUs, affinity)
	if err != nil {
		return &smtAwarePolicy{}, err
	}
	p, ok := pol.(*staticPolicy)
	if !ok {
		klog.ErrorS(err, "unexpected policy type, initialization of smtaware policy requires a static policy")
		return &smtAwarePolicy{}, err
	}
	return &smtAwarePolicy{p}, nil
}

func (p *smtAwarePolicy) Name() string {
	return string(PolicySMTAware)
}

func (p *smtAwarePolicy) Admit(pod *v1.Pod) lifecycle.PodAdmitResult {
	cpusPerCore := p.topology.CPUsPerCore()
	for _, container := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
		if requestedCPUs := p.guaranteedCPUs(pod, &container); requestedCPUs > 0 && (requestedCPUs%cpusPerCore) != 0 {
			// Since CPU Manager has been enabled with `smtaware` policy, it means a guaranteed pod can only be admitted
			// if the CPU requested is a multiple of the number of virtual cpus per physical cores.
			// In case CPU request is not a multiple of the number of virtual cpus per physical cores the Pod will be put
			// in Failed state, with SMTAlignmentError as reason. Since the allocation happens in terms of physical cores
			// and the scheduler is responsible for ensuring that the workload goes to a node that has enough CPUs,
			// the pod would be placed on a node where there are enough physical cores available to be allocated.
			// Just like the behaviour in case of static policy, takeByTopology will try to first allocate CPUs from the same socket
			// and only in case the request cannot be sattisfied on a single socket, CPU allocation is done for a workload to occupy all
			// CPUs on a physical core. Allocation of individual threads would never have to occur.
			return smtAlignmentError(requestedCPUs, cpusPerCore)
		}
	}
	return admitPod()
}
