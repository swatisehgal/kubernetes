/*
Copyright 2020 The Kubernetes Authors.

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
	"fmt"

	"sync"

	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
)

const (
	// containerTopologyScope specifies the TopologyManagerScope per container.
	containerTopologyScope = "container"
	// podTopologyScope specifies the TopologyManagerScope per pod.
	podTopologyScope = "pod"
)

type podTopologyHints map[string]map[string]TopologyHint

// Scope interface for Topology Manager
type Scope interface {
	Name() string
	Admit(pod *v1.Pod) lifecycle.PodAdmitResult
	// AddHintProvider adds a hint provider to manager to indicate the hint provider
	// wants to be consoluted with when making topology hints
	AddHintProvider(h HintProvider)
	// AddContainer adds pod to Manager for tracking
	AddContainer(pod *v1.Pod, containerID string) error
	// RemoveContainer removes pod from Manager tracking
	RemoveContainer(containerID string) error
	// Store is the interface for storing pod topology hints
	Store
}

type scope struct {
	mutex sync.Mutex
	name  string
	// Mapping of a Pods mapping of Containers and their TopologyHints
	// Indexed by PodUID to ContainerName
	podTopologyHints podTopologyHints
	// The list of components registered with the Manager
	hintProviders []HintProvider
	// Topology Manager Policy
	policy Policy
	// Mapping of PodUID to ContainerID for Adding/Removing Pods from PodTopologyHints mapping
	podMap map[string]string
}

func (s *scope) Name() string {
	return s.name
}

func (s *scope) getTopologyHints(podUID string, containerName string) TopologyHint {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.podTopologyHints[podUID][containerName]
}

func (s *scope) setTopologyHints(podUID string, containerName string, th TopologyHint) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.podTopologyHints[podUID] == nil {
		s.podTopologyHints[podUID] = make(map[string]TopologyHint)
	}
	s.podTopologyHints[podUID][containerName] = th
}

func (s *scope) GetAffinity(podUID string, containerName string) TopologyHint {
	return s.getTopologyHints(podUID, containerName)
}

func (s *scope) AddHintProvider(h HintProvider) {
	s.hintProviders = append(s.hintProviders, h)
}

// It would be better to implement this function in topologymanager instead of scope
// but topologymanager do not track mapping anymore
func (s *scope) AddContainer(pod *v1.Pod, containerID string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.podMap[containerID] = string(pod.UID)
	return nil
}

// It would be better to implement this function in topologymanager instead of scope
// but topologymanager do not track mapping anymore
func (s *scope) RemoveContainer(containerID string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	klog.InfoS("RemoveContainer", "containerID", containerID)
	podUIDString := s.podMap[containerID]
	delete(s.podMap, containerID)
	if _, exists := s.podTopologyHints[podUIDString]; exists {
		delete(s.podTopologyHints[podUIDString], containerID)
		if len(s.podTopologyHints[podUIDString]) == 0 {
			delete(s.podTopologyHints, podUIDString)
		}
	}

	return nil
}

func (s *scope) admitPolicyNone(pod *v1.Pod) lifecycle.PodAdmitResult {
	for _, container := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
		err := s.allocateAlignedResources(pod, &container)
		if err != nil {
			reason := "UnexpectedAdmissionError"
			if _, ok := err.(*SMTAlignmentError); ok {
				reason = "SMTAlignmentError"
				klog.InfoS("SMTAwareRequire container manager linux Matching,", "reason", reason)
			}
			return unexpectedAdmissionError(err, reason)
		}
	}
	return admitPod()
}

// It would be better to implement this function in topologymanager instead of scope
// but topologymanager do not track providers anymore
func (s *scope) allocateAlignedResources(pod *v1.Pod, container *v1.Container) error {
	for _, provider := range s.hintProviders {
		err := provider.Allocate(pod, container)
		if err != nil {
			return err
		}
	}
	return nil
}

func topologyAffinityError() lifecycle.PodAdmitResult {
	return lifecycle.PodAdmitResult{
		Message: "Resources cannot be allocated with Topology locality",
		Reason:  "TopologyAffinityError",
		Admit:   false,
	}
}

func unexpectedAdmissionError(err error, reason string) lifecycle.PodAdmitResult {
	return lifecycle.PodAdmitResult{
		Message: fmt.Sprintf("Allocate failed due to %v, which is unexpected", err),
		Reason:  reason,
		Admit:   false,
	}
}

func admitPod() lifecycle.PodAdmitResult {
	return lifecycle.PodAdmitResult{Admit: true}
}

type SMTAlignmentError struct {
	requestedCPUs int
	cpusPerCore   int
}

func (e *SMTAlignmentError) Error() string {
	errorMessage := fmt.Sprintf("SMTAlignmentError: Number of CPUs requested should be a multiple of number of CPUs on a core = %d on this system. Requested CPU count = %d", e.requestedCPUs, e.cpusPerCore)
	return errorMessage
}
func NewSMTAlignmentError(requestedCPUCount, cpusPerCoreCount int) error {
	return &SMTAlignmentError{
		requestedCPUs: requestedCPUCount,
		cpusPerCore:   cpusPerCoreCount,
	}
}
