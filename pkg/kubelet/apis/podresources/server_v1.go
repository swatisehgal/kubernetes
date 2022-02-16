/*
Copyright 2018 The Kubernetes Authors.

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

package podresources

import (
	"context"
	"fmt"
	"sync"

	utilfeature "k8s.io/apiserver/pkg/util/feature"
	kubefeatures "k8s.io/kubernetes/pkg/features"
	"k8s.io/kubernetes/pkg/kubelet/metrics"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/kubelet/pkg/apis/podresources/v1"
)

// podResourcesServerV1alpha1 implements PodResourcesListerServer
type v1PodResourcesServer struct {
	podsProvider    PodsProvider
	devicesProvider DevicesProvider
	cpusProvider    CPUsProvider
	memoryProvider  MemoryProvider
	podSource       chan podInfo
	lock            sync.RWMutex
	sinkId          int
	podSinks        map[int]chan podInfo
}

type podInfo struct {
	Action v1.WatchPodAction
	Pod    *corev1.Pod
}

// NewV1PodResourcesServer returns a PodResourcesListerServer which lists pods provided by the PodsProvider
// with device information provided by the DevicesProvider
func NewV1PodResourcesServer(podsProvider PodsProvider, devicesProvider DevicesProvider, cpusProvider CPUsProvider, memoryProvider MemoryProvider) v1.PodResourcesListerServer {
	p := &v1PodResourcesServer{
		podsProvider:    podsProvider,
		devicesProvider: devicesProvider,
		cpusProvider:    cpusProvider,
		memoryProvider:  memoryProvider,
		podSource:       make(chan podInfo),
		podSinks:        make(map[int]chan podInfo),
	}
	go p.dispatchPods()
	return p
}

func (p *v1PodResourcesServer) makePodResources(pod *corev1.Pod) *v1.PodResources {
	pRes := v1.PodResources{
		Name:       pod.Name,
		Namespace:  pod.Namespace,
		Containers: make([]*v1.ContainerResources, len(pod.Spec.Containers)),
	}

	for j, container := range pod.Spec.Containers {
		pRes.Containers[j] = &v1.ContainerResources{
			Name:    container.Name,
			Devices: p.devicesProvider.GetDevices(string(pod.UID), container.Name),
			CpuIds:  p.cpusProvider.GetCPUs(string(pod.UID), container.Name),
			Memory:  p.memoryProvider.GetMemory(string(pod.UID), container.Name),
		}
	}
	return &pRes
}

// List returns information about the resources assigned to pods on the node
func (p *v1PodResourcesServer) List(ctx context.Context, req *v1.ListPodResourcesRequest) (*v1.ListPodResourcesResponse, error) {
	metrics.PodResourcesEndpointRequestsTotalCount.WithLabelValues("v1").Inc()
	metrics.PodResourcesEndpointRequestsListCount.WithLabelValues("v1").Inc()

	pods := p.podsProvider.GetPods()
	podResources := make([]*v1.PodResources, len(pods))
	p.devicesProvider.UpdateAllocatedDevices()

	for i, pod := range pods {
		pRes := v1.PodResources{
			Name:       pod.Name,
			Namespace:  pod.Namespace,
			Containers: make([]*v1.ContainerResources, len(pod.Spec.Containers)),
		}

		for j, container := range pod.Spec.Containers {
			pRes.Containers[j] = &v1.ContainerResources{
				Name:    container.Name,
				Devices: p.devicesProvider.GetDevices(string(pod.UID), container.Name),
				CpuIds:  p.cpusProvider.GetCPUs(string(pod.UID), container.Name),
				Memory:  p.memoryProvider.GetMemory(string(pod.UID), container.Name),
			}
		}
		podResources[i] = &pRes
	}

	return &v1.ListPodResourcesResponse{
		PodResources: podResources,
	}, nil
}

// GetAllocatableResources returns information about all the resources known by the server - this more like the capacity, not like the current amount of free resources.
func (p *v1PodResourcesServer) GetAllocatableResources(ctx context.Context, req *v1.AllocatableResourcesRequest) (*v1.AllocatableResourcesResponse, error) {
	metrics.PodResourcesEndpointRequestsTotalCount.WithLabelValues("v1").Inc()
	metrics.PodResourcesEndpointRequestsGetAllocatableCount.WithLabelValues("v1").Inc()

	if !utilfeature.DefaultFeatureGate.Enabled(kubefeatures.KubeletPodResourcesGetAllocatable) {
		metrics.PodResourcesEndpointErrorsGetAllocatableCount.WithLabelValues("v1").Inc()
		return nil, fmt.Errorf("Pod Resources API GetAllocatableResources disabled")
	}

	metrics.PodResourcesEndpointRequestsTotalCount.WithLabelValues("v1").Inc()

	return &v1.AllocatableResourcesResponse{
		Devices: p.devicesProvider.GetAllocatableDevices(),
		CpuIds:  p.cpusProvider.GetAllocatableCPUs(),
		Memory:  p.memoryProvider.GetAllocatableMemory(),
	}, nil
}

func (p *v1PodResourcesServer) ListAndWatch(req *v1.ListAndWatchPodResourcesRequest, srv v1.PodResourcesLister_ListAndWatchServer) error {
	sinkId, sinkChan := p.registerWatcher()
	defer p.unregisterWatcher(sinkId)
	for {
		pod := <-sinkChan
		resp := p.makeWatchPodResponse(pod)
		err := srv.Send(resp)
		if err != nil {
			return err
		}
	}
	return nil
}

func (p *v1PodResourcesServer) AddPod(pod *corev1.Pod) {
	p.podSource <- podInfo{
		Action: v1.WatchPodAction_ADDED,
		Pod:    pod,
	}
}

func (p *v1PodResourcesServer) UpdatePod(pod *corev1.Pod) {
	p.podSource <- podInfo{
		Action: v1.WatchPodAction_UPDATED,
		Pod:    pod,
	}
}

func (p *v1PodResourcesServer) DeletePod(pod *corev1.Pod) {
	p.podSource <- podInfo{
		Action: v1.WatchPodAction_DELETED,
		Pod:    pod,
	}
}

func (p *v1PodResourcesServer) dispatchPods() {
	for {
		info := <-p.podSource

		p.lock.RLock()
		for _, ch := range p.podSinks {
			ch <- info
		}
		p.lock.RUnlock()
	}
}

func (p *v1PodResourcesServer) registerWatcher() (int, chan podInfo) {
	p.lock.Lock()
	defer p.lock.Unlock()
	sinkChan := make(chan podInfo)
	sinkId := p.sinkId
	p.sinkId++
	p.podSinks[sinkId] = sinkChan
	return sinkId, sinkChan
}

func (p *v1PodResourcesServer) unregisterWatcher(sinkId int) {
	p.lock.Lock()
	defer p.lock.Unlock()
	// TODO: sink close?
	delete(p.podSinks, sinkId)
}

func (p *v1PodResourcesServer) makeWatchPodResponse(info podInfo) *v1.ListAndWatchPodResourcesResponse {
	resp := v1.ListAndWatchPodResourcesResponse{
		Action: info.Action,
		PodResources: []*v1.PodResources{
			p.makePodResources(info.Pod),
		},
	}
	return &resp
}
