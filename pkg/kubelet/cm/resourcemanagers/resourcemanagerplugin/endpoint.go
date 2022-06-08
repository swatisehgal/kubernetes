/*
Copyright 2022 The Kubernetes Authors.

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

package resourcemanagerplugin

import (
	"context"
	"fmt"
	"sync"
	"time"

	pluginapi "k8s.io/kubelet/pkg/apis/resourceplugin/v1alpha"
	plugin "k8s.io/kubernetes/pkg/kubelet/cm/resourcemanagers/resourcemanagerplugin/v1alpha"
)

// endpoint maps to a single registered resource plugin. It is responsible
// for managing gRPC communications with the resource plugin and caching
// resource states reported by the resource plugin.
type endpoint interface {
	getPreferredAllocation(available, mustInclude []string, size int) (*pluginapi.PreferredAllocationResponse, error)
	allocate(devs []string) (*pluginapi.AllocateResponse, error)
	preStartContainer(devs []string) (*pluginapi.PreStartContainerResponse, error)
	setStopTime(t time.Time)
	isStopped() bool
	stopGracePeriodExpired() bool
}

type endpointImpl struct {
	mutex        sync.Mutex
	resourceName string
	api          pluginapi.ResourcePluginClient
	stopTime     time.Time
	client       plugin.Client // for testing only
}

// newEndpointImpl creates a new endpoint for the given resourceName.
// This is to be used during normal resource plugin registration.
func newEndpointImpl(p plugin.ResourcePlugin) *endpointImpl {
	return &endpointImpl{
		api:          p.Api(),
		resourceName: p.Resource(),
	}
}

// newStoppedEndpointImpl creates a new endpoint for the given resourceName with stopTime set.
// This is to be used during Kubelet restart, before the actual resource plugin re-registers.
func newStoppedEndpointImpl(resourceName string) *endpointImpl {
	return &endpointImpl{
		resourceName: resourceName,
		stopTime:     time.Now(),
	}
}

func (e *endpointImpl) isStopped() bool {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	return !e.stopTime.IsZero()
}

func (e *endpointImpl) stopGracePeriodExpired() bool {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	return !e.stopTime.IsZero() && time.Since(e.stopTime) > endpointStopGracePeriod
}

func (e *endpointImpl) setStopTime(t time.Time) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	e.stopTime = t
}

// getPreferredAllocation issues GetPreferredAllocation gRPC call to the resource plugin.
func (e *endpointImpl) getPreferredAllocation(available, mustInclude []string, size int) (*pluginapi.PreferredAllocationResponse, error) {
	if e.isStopped() {
		return nil, fmt.Errorf(errEndpointStopped, e)
	}
	return e.api.GetPreferredAllocation(context.Background(), &pluginapi.PreferredAllocationRequest{
		ContainerRequests: []*pluginapi.ContainerPreferredAllocationRequest{
			{
				AvailableResourceIDs:   available,
				MustIncludeResourceIDs: mustInclude,
				AllocationSize:         int32(size),
			},
		},
	})
}

// allocate issues Allocate gRPC call to the resource plugin.
func (e *endpointImpl) allocate(devs []string) (*pluginapi.AllocateResponse, error) {
	if e.isStopped() {
		return nil, fmt.Errorf(errEndpointStopped, e)
	}
	return e.api.Allocate(context.Background(), &pluginapi.AllocateRequest{
		ContainerRequests: []*pluginapi.ContainerAllocateRequest{
			{ResourcesIDs: devs},
		},
	})
}

// preStartContainer issues PreStartContainer gRPC call to the resource plugin.
func (e *endpointImpl) preStartContainer(devs []string) (*pluginapi.PreStartContainerResponse, error) {
	if e.isStopped() {
		return nil, fmt.Errorf(errEndpointStopped, e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), pluginapi.KubeletPreStartContainerRPCTimeoutInSecs*time.Second)
	defer cancel()
	return e.api.PreStartContainer(ctx, &pluginapi.PreStartContainerRequest{
		ResourcesIDs: devs,
	})
}
