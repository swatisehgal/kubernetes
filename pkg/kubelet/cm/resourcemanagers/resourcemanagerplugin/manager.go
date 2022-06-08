/*
Copyright 2017 The Kubernetes Authors.

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
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"

	cadvisorapi "github.com/google/cadvisor/info/v1"
	"k8s.io/klog/v2"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	errorsutil "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	pluginapi "k8s.io/kubelet/pkg/apis/resourceplugin/v1alpha"
	"k8s.io/kubernetes/pkg/features"
	"k8s.io/kubernetes/pkg/kubelet/checkpointmanager"
	"k8s.io/kubernetes/pkg/kubelet/checkpointmanager/errors"
	"k8s.io/kubernetes/pkg/kubelet/cm/resourcemanagers/resourcemanagerplugin/checkpoint"
	plugin "k8s.io/kubernetes/pkg/kubelet/cm/resourcemanagers/resourcemanagerplugin/plugin/v1alpha"
	"k8s.io/kubernetes/pkg/kubelet/cm/resourcemanagers/topologymanager"
	"k8s.io/kubernetes/pkg/kubelet/config"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
	"k8s.io/kubernetes/pkg/kubelet/metrics"
	"k8s.io/kubernetes/pkg/kubelet/pluginmanager/cache"
	schedulerframework "k8s.io/kubernetes/pkg/scheduler/framework"
)

const nodeWithoutTopology = -1

// ActivePodsFunc is a function that returns a list of pods to reconcile.
type ActivePodsFunc func() []*v1.Pod

// ManagerImpl is the structure in charge of managing Device Plugins.
type ManagerImpl struct {
	checkpointdir string

	endpoints map[string]endpointInfo // Key is ResourceName
	mutex     sync.Mutex

	server plugin.Server

	// activePods is a method for listing active pods on the node
	// so the amount of pluginResources requested by existing pods
	// could be counted when updating allocated resources
	activePods ActivePodsFunc

	// sourcesReady provides the readiness of kubelet configuration sources such as apiserver update readiness.
	// We use it to determine when we can purge inactive pods from checkpointed state.
	sourcesReady config.SourcesReady

	// allResources holds all the resources currently registered to the device manager
	allResources ResourceDeviceInstances

	// healthyResources contains all of the registered healthy resourceNames and their exported device IDs.
	healthyResources map[string]sets.String

	// unhealthyResources contains all of the unhealthy resources and their exported device IDs.
	unhealthyResources map[string]sets.String

	// allocatedResources contains allocated deviceIds, keyed by resourceName.
	allocatedResources map[string]sets.String

	// podResources contains pod to allocated device mapping.
	podResources      *podResources
	checkpointManager checkpointmanager.CheckpointManager

	// List of NUMA Nodes available on the underlying machine
	numaNodes []int

	// Store of Topology Affinties that the Device Manager can query.
	topologyAffinityStore topologymanager.Store

	// resourcesToReuse contains resources that can be reused as they have been allocated to
	// init containers.
	resourcesToReuse PodReusableResources

	// pendingAdmissionPod contain the pod during the admission phase
	pendingAdmissionPod *v1.Pod
}

type endpointInfo struct {
	e    endpoint
	opts *pluginapi.ResourcePluginOptions
}

type sourcesReadyStub struct{}

// PodReusableResources is a map by pod name of resources to reuse.
type PodReusableResources map[string]map[string]sets.String

func (s *sourcesReadyStub) AddSource(source string) {}
func (s *sourcesReadyStub) AllReady() bool          { return true }

// NewManagerImpl creates a new manager.
func NewManagerImpl(topology []cadvisorapi.Node, topologyAffinityStore topologymanager.Store) (*ManagerImpl, error) {
	socketPath := pluginapi.KubeletSocket
	if runtime.GOOS == "windows" {
		socketPath = os.Getenv("SYSTEMDRIVE") + pluginapi.KubeletSocketWindows
	}
	return newManagerImpl(socketPath, topology, topologyAffinityStore)
}

func newManagerImpl(socketPath string, topology []cadvisorapi.Node, topologyAffinityStore topologymanager.Store) (*ManagerImpl, error) {
	klog.V(2).InfoS("Creating Device Plugin manager", "path", socketPath)

	var numaNodes []int
	for _, node := range topology {
		numaNodes = append(numaNodes, node.Id)
	}

	manager := &ManagerImpl{
		endpoints: make(map[string]endpointInfo),

		allResources:          NewResourceDeviceInstances(),
		healthyResources:      make(map[string]sets.String),
		unhealthyResources:    make(map[string]sets.String),
		allocatedResources:    make(map[string]sets.String),
		podResources:          newPodResources(),
		numaNodes:             numaNodes,
		topologyAffinityStore: topologyAffinityStore,
		resourcesToReuse:      make(PodReusableResources),
	}

	server, err := plugin.NewServer(socketPath, manager, manager)
	if err != nil {
		return nil, fmt.Errorf("failed to create plugin server: %v", err)
	}

	manager.server = server
	manager.checkpointdir, _ = filepath.Split(server.SocketPath())

	// The following structures are populated with real implementations in manager.Start()
	// Before that, initializes them to perform no-op operations.
	manager.activePods = func() []*v1.Pod { return []*v1.Pod{} }
	manager.sourcesReady = &sourcesReadyStub{}
	checkpointManager, err := checkpointmanager.NewCheckpointManager(manager.checkpointdir)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize checkpoint manager: %v", err)
	}
	manager.checkpointManager = checkpointManager

	return manager, nil
}

func (m *ManagerImpl) CleanupPluginDirectory(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	names, err := d.Readdirnames(-1)
	if err != nil {
		return err
	}
	var errs []error
	for _, name := range names {
		filePath := filepath.Join(dir, name)
		if filePath == m.checkpointFile() {
			continue
		}
		// TODO: Until the bug - https://github.com/golang/go/issues/33357 is fixed, os.stat wouldn't return the
		// right mode(socket) on windows. Hence deleting the file, without checking whether
		// its a socket, on windows.
		stat, err := os.Lstat(filePath)
		if err != nil {
			klog.ErrorS(err, "Failed to stat file", "path", filePath)
			continue
		}
		if stat.IsDir() {
			continue
		}
		err = os.RemoveAll(filePath)
		if err != nil {
			errs = append(errs, err)
			klog.ErrorS(err, "Failed to remove file", "path", filePath)
			continue
		}
	}
	return errorsutil.NewAggregate(errs)
}

func (m *ManagerImpl) PluginConnected(resourceName string, p plugin.DevicePlugin) error {
	options, err := p.Api().GetDevicePluginOptions(context.Background(), &pluginapi.Empty{})
	if err != nil {
		return fmt.Errorf("failed to get device plugin options: %v", err)
	}

	e := newEndpointImpl(p)

	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.endpoints[resourceName] = endpointInfo{e, options}

	return nil
}

func (m *ManagerImpl) PluginDisconnected(resourceName string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if _, exists := m.endpoints[resourceName]; exists {
		m.markResourceUnhealthy(resourceName)
		klog.V(2).InfoS("Endpoint became unhealthy", "resourceName", resourceName, "endpoint", m.endpoints[resourceName])
	}

	m.endpoints[resourceName].e.setStopTime(time.Now())
}

func (m *ManagerImpl) PluginListAndWatchReceiver(resourceName string, resp *pluginapi.ListAndWatchResponse) {
	var resources []pluginapi.Resource
	for _, d := range resp.Resources {
		resources = append(resources, *d)
	}
	m.genericDeviceUpdateCallback(resourceName, resources)
}

func (m *ManagerImpl) genericDeviceUpdateCallback(resourceName string, resources []pluginapi.Device) {
	m.mutex.Lock()
	m.healthyResources[resourceName] = sets.NewString()
	m.unhealthyResources[resourceName] = sets.NewString()
	m.allResources[resourceName] = make(map[string]pluginapi.Device)
	for _, dev := range resources {
		m.allResources[resourceName][dev.ID] = dev
		if dev.Health == pluginapi.Healthy {
			m.healthyResources[resourceName].Insert(dev.ID)
		} else {
			m.unhealthyResources[resourceName].Insert(dev.ID)
		}
	}
	m.mutex.Unlock()
	if err := m.writeCheckpoint(); err != nil {
		klog.ErrorS(err, "Writing checkpoint encountered")
	}
}

// GetWatcherHandler returns the plugin handler
func (m *ManagerImpl) GetWatcherHandler() cache.PluginHandler {
	return m.server
}

// checkpointFile returns device plugin checkpoint file path.
func (m *ManagerImpl) checkpointFile() string {
	return filepath.Join(m.checkpointdir, kubeletDeviceManagerCheckpoint)
}

// Start starts the Device Plugin Manager and start initialization of
// podResources and allocatedResources information from checkpointed state and
// starts device plugin registration service.
func (m *ManagerImpl) Start(activePods ActivePodsFunc, sourcesReady config.SourcesReady) error {
	klog.V(2).InfoS("Starting Device Plugin manager")

	m.activePods = activePods
	m.sourcesReady = sourcesReady

	// Loads in allocatedResources information from disk.
	err := m.readCheckpoint()
	if err != nil {
		klog.InfoS("Continue after failing to read checkpoint file. Device allocation info may NOT be up-to-date", "err", err)
	}

	return m.server.Start()
}

// Stop is the function that can stop the plugin server.
// Can be called concurrently, more than once, and is safe to call
// without a prior Start.
func (m *ManagerImpl) Stop() error {
	return m.server.Stop()
}

// Allocate is the call that you can use to allocate a set of resources
// from the registered device plugins.
func (m *ManagerImpl) Allocate(pod *v1.Pod, container *v1.Container) error {
	// The pod is during the admission phase. We need to save the pod to avoid it
	// being cleaned before the admission ended
	m.setPodPendingAdmission(pod)

	if _, ok := m.resourcesToReuse[string(pod.UID)]; !ok {
		m.resourcesToReuse[string(pod.UID)] = make(map[string]sets.String)
	}
	// If pod entries to m.resourcesToReuse other than the current pod exist, delete them.
	for podUID := range m.resourcesToReuse {
		if podUID != string(pod.UID) {
			delete(m.resourcesToReuse, podUID)
		}
	}
	// Allocate resources for init containers first as we know the caller always loops
	// through init containers before looping through app containers. Should the caller
	// ever change those semantics, this logic will need to be amended.
	for _, initContainer := range pod.Spec.InitContainers {
		if container.Name == initContainer.Name {
			if err := m.allocateContainerResources(pod, container, m.resourcesToReuse[string(pod.UID)]); err != nil {
				return err
			}
			m.podResources.addContainerAllocatedResources(string(pod.UID), container.Name, m.resourcesToReuse[string(pod.UID)])
			return nil
		}
	}
	if err := m.allocateContainerResources(pod, container, m.resourcesToReuse[string(pod.UID)]); err != nil {
		return err
	}
	m.podResources.removeContainerAllocatedResources(string(pod.UID), container.Name, m.resourcesToReuse[string(pod.UID)])
	return nil
}

// UpdatePluginResources updates node resources based on resources already allocated to pods.
func (m *ManagerImpl) UpdatePluginResources(node *schedulerframework.NodeInfo, attrs *lifecycle.PodAdmitAttributes) error {
	pod := attrs.Pod

	// quick return if no pluginResources requested
	if !m.podResources.hasPod(string(pod.UID)) {
		return nil
	}

	m.sanitizeNodeAllocatable(node)
	return nil
}

func (m *ManagerImpl) markResourceUnhealthy(resourceName string) {
	klog.V(2).InfoS("Mark all resources Unhealthy for resource", "resourceName", resourceName)
	healthyResources := sets.NewString()
	if _, ok := m.healthyResources[resourceName]; ok {
		healthyResources = m.healthyResources[resourceName]
		m.healthyResources[resourceName] = sets.NewString()
	}
	if _, ok := m.unhealthyResources[resourceName]; !ok {
		m.unhealthyResources[resourceName] = sets.NewString()
	}
	m.unhealthyResources[resourceName] = m.unhealthyResources[resourceName].Union(healthyResources)
}

// GetCapacity is expected to be called when Kubelet updates its node status.
// The first returned variable contains the registered device plugin resource capacity.
// The second returned variable contains the registered device plugin resource allocatable.
// The third returned variable contains previously registered resources that are no longer active.
// Kubelet uses this information to update resource capacity/allocatable in its node status.
// After the call, device plugin can remove the inactive resources from its internal list as the
// change is already reflected in Kubelet node status.
// Note in the special case after Kubelet restarts, device plugin resource capacities can
// temporarily drop to zero till corresponding device plugins re-register. This is OK because
// cm.UpdatePluginResource() run during predicate Admit guarantees we adjust nodeinfo
// capacity for already allocated pods so that they can continue to run. However, new pods
// requiring device plugin resources will not be scheduled till device plugin re-registers.
func (m *ManagerImpl) GetCapacity() (v1.ResourceList, v1.ResourceList, []string) {
	needsUpdateCheckpoint := false
	var capacity = v1.ResourceList{}
	var allocatable = v1.ResourceList{}
	deletedResources := sets.NewString()
	m.mutex.Lock()
	for resourceName, resources := range m.healthyResources {
		eI, ok := m.endpoints[resourceName]
		if (ok && eI.e.stopGracePeriodExpired()) || !ok {
			// The resources contained in endpoints and (un)healthyResources
			// should always be consistent. Otherwise, we run with the risk
			// of failing to garbage collect non-existing resources or resources.
			if !ok {
				klog.ErrorS(nil, "Unexpected: healthyResources and endpoints are out of sync")
			}
			delete(m.endpoints, resourceName)
			delete(m.healthyResources, resourceName)
			deletedResources.Insert(resourceName)
			needsUpdateCheckpoint = true
		} else {
			capacity[v1.ResourceName(resourceName)] = *resource.NewQuantity(int64(resources.Len()), resource.DecimalSI)
			allocatable[v1.ResourceName(resourceName)] = *resource.NewQuantity(int64(resources.Len()), resource.DecimalSI)
		}
	}
	for resourceName, resources := range m.unhealthyResources {
		eI, ok := m.endpoints[resourceName]
		if (ok && eI.e.stopGracePeriodExpired()) || !ok {
			if !ok {
				klog.ErrorS(nil, "Unexpected: unhealthyResources and endpoints are out of sync")
			}
			delete(m.endpoints, resourceName)
			delete(m.unhealthyResources, resourceName)
			deletedResources.Insert(resourceName)
			needsUpdateCheckpoint = true
		} else {
			capacityCount := capacity[v1.ResourceName(resourceName)]
			unhealthyCount := *resource.NewQuantity(int64(resources.Len()), resource.DecimalSI)
			capacityCount.Add(unhealthyCount)
			capacity[v1.ResourceName(resourceName)] = capacityCount
		}
	}
	m.mutex.Unlock()
	if needsUpdateCheckpoint {
		if err := m.writeCheckpoint(); err != nil {
			klog.ErrorS(err, "Error on writing checkpoint")
		}
	}
	return capacity, allocatable, deletedResources.UnsortedList()
}

// Checkpoints device to container allocation information to disk.
func (m *ManagerImpl) writeCheckpoint() error {
	m.mutex.Lock()
	registeredDevs := make(map[string][]string)
	for resource, resources := range m.healthyResources {
		registeredDevs[resource] = resources.UnsortedList()
	}
	data := checkpoint.New(m.podResources.toCheckpointData(),
		registeredDevs)
	m.mutex.Unlock()
	err := m.checkpointManager.CreateCheckpoint(kubeletDeviceManagerCheckpoint, data)
	if err != nil {
		err2 := fmt.Errorf("failed to write checkpoint file %q: %v", kubeletDeviceManagerCheckpoint, err)
		klog.InfoS("Failed to write checkpoint file", "err", err)
		return err2
	}
	return nil
}

// Reads device to container allocation information from disk, and populates
// m.allocatedResources accordingly.
func (m *ManagerImpl) readCheckpoint() error {
	// the vast majority of time we restore a compatible checkpoint, so we try
	// the current version first. Trying to restore older format checkpoints is
	// relevant only in the kubelet upgrade flow, which happens once in a
	// (long) while.
	cp, err := m.getCheckpointV2()
	if err != nil {
		if err == errors.ErrCheckpointNotFound {
			// no point in trying anything else
			klog.InfoS("Failed to read data from checkpoint", "checkpoint", kubeletDeviceManagerCheckpoint, "err", err)
			return nil
		}

		var errv1 error
		// one last try: maybe it's a old format checkpoint?
		cp, errv1 = m.getCheckpointV1()
		if errv1 != nil {
			klog.InfoS("Failed to read checkpoint V1 file", "err", errv1)
			// intentionally return the parent error. We expect to restore V1 checkpoints
			// a tiny fraction of time, so what matters most is the current checkpoint read error.
			return err
		}
		klog.InfoS("Read data from a V1 checkpoint", "checkpoint", kubeletDeviceManagerCheckpoint)
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()
	podResources, registeredDevs := cp.GetDataInLatestFormat()
	m.podResources.fromCheckpointData(podResources)
	m.allocatedResources = m.podResources.resources()
	for resource := range registeredDevs {
		// During start up, creates empty healthyResources list so that the resource capacity
		// will stay zero till the corresponding device plugin re-registers.
		m.healthyResources[resource] = sets.NewString()
		m.unhealthyResources[resource] = sets.NewString()
		m.endpoints[resource] = endpointInfo{e: newStoppedEndpointImpl(resource), opts: nil}
	}
	return nil
}

func (m *ManagerImpl) getCheckpointV2() (checkpoint.DeviceManagerCheckpoint, error) {
	registeredDevs := make(map[string][]string)
	devEntries := make([]checkpoint.PodResourcesEntry, 0)
	cp := checkpoint.New(devEntries, registeredDevs)
	err := m.checkpointManager.GetCheckpoint(kubeletDeviceManagerCheckpoint, cp)
	return cp, err
}

func (m *ManagerImpl) getCheckpointV1() (checkpoint.DeviceManagerCheckpoint, error) {
	registeredDevs := make(map[string][]string)
	devEntries := make([]checkpoint.PodResourcesEntryV1, 0)
	cp := checkpoint.NewV1(devEntries, registeredDevs)
	err := m.checkpointManager.GetCheckpoint(kubeletDeviceManagerCheckpoint, cp)
	return cp, err
}

// UpdateAllocatedResources frees any Resources that are bound to terminated pods.
func (m *ManagerImpl) UpdateAllocatedResources() {
	if !m.sourcesReady.AllReady() {
		return
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	activeAndAdmittedPods := m.activePods()
	if m.pendingAdmissionPod != nil {
		activeAndAdmittedPods = append(activeAndAdmittedPods, m.pendingAdmissionPod)
	}

	podsToBeRemoved := m.podResources.pods()
	for _, pod := range activeAndAdmittedPods {
		podsToBeRemoved.Delete(string(pod.UID))
	}
	if len(podsToBeRemoved) <= 0 {
		return
	}
	klog.V(3).InfoS("Pods to be removed", "podUIDs", podsToBeRemoved.List())
	m.podResources.delete(podsToBeRemoved.List())
	// Regenerated allocatedResources after we update pod allocation information.
	m.allocatedResources = m.podResources.resources()
}

// Returns list of device Ids we need to allocate with Allocate rpc call.
// Returns empty list in case we don't need to issue the Allocate rpc call.
func (m *ManagerImpl) resourcesToAllocate(podUID, contName, resource string, required int, reusableResources sets.String) (sets.String, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	needed := required
	// Gets list of resources that have already been allocated.
	// This can happen if a container restarts for example.
	resources := m.podResources.containerResources(podUID, contName, resource)
	if resources != nil {
		klog.V(3).InfoS("Found pre-allocated resources for resource on pod", "resourceName", resource, "containerName", contName, "podUID", string(podUID), "resources", resources.List())
		needed = needed - resources.Len()
		// A pod's resource is not expected to change once admitted by the API server,
		// so just fail loudly here. We can revisit this part if this no longer holds.
		if needed != 0 {
			return nil, fmt.Errorf("pod %q container %q changed request for resource %q from %d to %d", string(podUID), contName, resource, resources.Len(), required)
		}
	}
	if needed == 0 {
		// No change, no work.
		return nil, nil
	}
	klog.V(3).InfoS("Need resources to allocate for pod", "deviceNumber", needed, "resourceName", resource, "podUID", string(podUID), "containerName", contName)
	// Check if resource registered with devicemanager
	if _, ok := m.healthyResources[resource]; !ok {
		return nil, fmt.Errorf("can't allocate unregistered device %s", resource)
	}

	// Declare the list of allocated resources.
	// This will be populated and returned below.
	allocated := sets.NewString()

	// Create a closure to help with device allocation
	// Returns 'true' once no more resources need to be allocated.
	allocateRemainingFrom := func(resources sets.String) bool {
		for device := range resources.Difference(allocated) {
			m.allocatedResources[resource].Insert(device)
			allocated.Insert(device)
			needed--
			if needed == 0 {
				return true
			}
		}
		return false
	}

	// Allocates from reusableResources list first.
	if allocateRemainingFrom(reusableResources) {
		return allocated, nil
	}

	// Needs to allocate additional resources.
	if m.allocatedResources[resource] == nil {
		m.allocatedResources[resource] = sets.NewString()
	}

	// Gets Resources in use.
	resourcesInUse := m.allocatedResources[resource]
	// Gets Available resources.
	available := m.healthyResources[resource].Difference(resourcesInUse)
	if available.Len() < needed {
		return nil, fmt.Errorf("requested number of resources unavailable for %s. Requested: %d, Available: %d", resource, needed, available.Len())
	}

	// Filters available Resources based on NUMA affinity.
	aligned, unaligned, noAffinity := m.filterByAffinity(podUID, contName, resource, available)

	// If we can allocate all remaining resources from the set of aligned ones, then
	// give the plugin the chance to influence which ones to allocate from that set.
	if needed < aligned.Len() {
		// First allocate from the preferred resources list (if available).
		preferred, err := m.callGetPreferredAllocationIfAvailable(podUID, contName, resource, aligned.Union(allocated), allocated, required)
		if err != nil {
			return nil, err
		}
		if allocateRemainingFrom(preferred.Intersection(aligned)) {
			return allocated, nil
		}
		// Then fallback to allocate from the aligned set if no preferred list
		// is returned (or not enough resources are returned in that list).
		if allocateRemainingFrom(aligned) {
			return allocated, nil
		}

		return nil, fmt.Errorf("unexpectedly allocated less resources than required. Requested: %d, Got: %d", required, required-needed)
	}

	// If we can't allocate all remaining resources from the set of aligned ones,
	// then start by first allocating all of the  aligned resources (to ensure
	// that the alignment guaranteed by the TopologyManager is honored).
	if allocateRemainingFrom(aligned) {
		return allocated, nil
	}

	// Then give the plugin the chance to influence the decision on any
	// remaining resources to allocate.
	preferred, err := m.callGetPreferredAllocationIfAvailable(podUID, contName, resource, available.Union(allocated), allocated, required)
	if err != nil {
		return nil, err
	}
	if allocateRemainingFrom(preferred.Intersection(available)) {
		return allocated, nil
	}

	// Finally, if the plugin did not return a preferred allocation (or didn't
	// return a large enough one), then fall back to allocating the remaining
	// resources from the 'unaligned' and 'noAffinity' sets.
	if allocateRemainingFrom(unaligned) {
		return allocated, nil
	}
	if allocateRemainingFrom(noAffinity) {
		return allocated, nil
	}

	return nil, fmt.Errorf("unexpectedly allocated less resources than required. Requested: %d, Got: %d", required, required-needed)
}

func (m *ManagerImpl) filterByAffinity(podUID, contName, resource string, available sets.String) (sets.String, sets.String, sets.String) {
	// If alignment information is not available, just pass the available list back.
	hint := m.topologyAffinityStore.GetAffinity(podUID, contName)
	if !m.deviceHasTopologyAlignment(resource) || hint.NUMANodeAffinity == nil {
		return sets.NewString(), sets.NewString(), available
	}

	// Build a map of NUMA Nodes to the resources associated with them. A
	// device may be associated to multiple NUMA nodes at the same time. If an
	// available device does not have any NUMA Nodes associated with it, add it
	// to a list of NUMA Nodes for the fake NUMANode -1.
	perNodeResources := make(map[int]sets.String)
	for d := range available {
		if m.allResources[resource][d].Topology == nil || len(m.allResources[resource][d].Topology.Nodes) == 0 {
			if _, ok := perNodeResources[nodeWithoutTopology]; !ok {
				perNodeResources[nodeWithoutTopology] = sets.NewString()
			}
			perNodeResources[nodeWithoutTopology].Insert(d)
			continue
		}

		for _, node := range m.allResources[resource][d].Topology.Nodes {
			if _, ok := perNodeResources[int(node.ID)]; !ok {
				perNodeResources[int(node.ID)] = sets.NewString()
			}
			perNodeResources[int(node.ID)].Insert(d)
		}
	}

	// Get a flat list of all of the nodes associated with available resources.
	var nodes []int
	for node := range perNodeResources {
		nodes = append(nodes, node)
	}

	// Sort the list of nodes by:
	// 1) Nodes contained in the 'hint's affinity set
	// 2) Nodes not contained in the 'hint's affinity set
	// 3) The fake NUMANode of -1 (assuming it is included in the list)
	// Within each of the groups above, sort the nodes by how many resources they contain
	sort.Slice(nodes, func(i, j int) bool {
		// If one or the other of nodes[i] or nodes[j] is in the 'hint's affinity set
		if hint.NUMANodeAffinity.IsSet(nodes[i]) && hint.NUMANodeAffinity.IsSet(nodes[j]) {
			return perNodeResources[nodes[i]].Len() < perNodeResources[nodes[j]].Len()
		}
		if hint.NUMANodeAffinity.IsSet(nodes[i]) {
			return true
		}
		if hint.NUMANodeAffinity.IsSet(nodes[j]) {
			return false
		}

		// If one or the other of nodes[i] or nodes[j] is the fake NUMA node -1 (they can't both be)
		if nodes[i] == nodeWithoutTopology {
			return false
		}
		if nodes[j] == nodeWithoutTopology {
			return true
		}

		// Otherwise both nodes[i] and nodes[j] are real NUMA nodes that are not in the 'hint's' affinity list.
		return perNodeResources[nodes[i]].Len() < perNodeResources[nodes[j]].Len()
	})

	// Generate three sorted lists of resources. Resources in the first list come
	// from valid NUMA Nodes contained in the affinity mask. Resources in the
	// second list come from valid NUMA Nodes not in the affinity mask. Resources
	// in the third list come from resources with no NUMA Node association (i.e.
	// those mapped to the fake NUMA Node -1). Because we loop through the
	// sorted list of NUMA nodes in order, within each list, resources are sorted
	// by their connection to NUMA Nodes with more resources on them.
	var fromAffinity []string
	var notFromAffinity []string
	var withoutTopology []string
	for d := range available {
		// Since the same device may be associated with multiple NUMA Nodes. We
		// need to be careful not to add each device to multiple lists. The
		// logic below ensures this by breaking after the first NUMA node that
		// has the device is encountered.
		for _, n := range nodes {
			if perNodeResources[n].Has(d) {
				if n == nodeWithoutTopology {
					withoutTopology = append(withoutTopology, d)
				} else if hint.NUMANodeAffinity.IsSet(n) {
					fromAffinity = append(fromAffinity, d)
				} else {
					notFromAffinity = append(notFromAffinity, d)
				}
				break
			}
		}
	}

	// Return all three lists containing the full set of resources across them.
	return sets.NewString(fromAffinity...), sets.NewString(notFromAffinity...), sets.NewString(withoutTopology...)
}

// allocateContainerResources attempts to allocate all of required device
// plugin resources for the input container, issues an Allocate rpc request
// for each new device resource requirement, processes their AllocateResponses,
// and updates the cached containerResources on success.
func (m *ManagerImpl) allocateContainerResources(pod *v1.Pod, container *v1.Container, resourcesToReuse map[string]sets.String) error {
	podUID := string(pod.UID)
	contName := container.Name
	allocatedResourcesUpdated := false
	needsUpdateCheckpoint := false
	// Extended resources are not allowed to be overcommitted.
	// Since device plugin advertises extended resources,
	// therefore Requests must be equal to Limits and iterating
	// over the Limits should be sufficient.
	for k, v := range container.Resources.Limits {
		resource := string(k)
		needed := int(v.Value())
		klog.V(3).InfoS("Looking for needed resources", "needed", needed, "resourceName", resource)
		if !m.isDevicePluginResource(resource) {
			continue
		}
		// Updates allocatedResources to garbage collect any stranded resources
		// before doing the device plugin allocation.
		if !allocatedResourcesUpdated {
			m.UpdateAllocatedResources()
			allocatedResourcesUpdated = true
		}
		allocResources, err := m.resourcesToAllocate(podUID, contName, resource, needed, resourcesToReuse[resource])
		if err != nil {
			return err
		}
		if allocResources == nil || len(allocResources) <= 0 {
			continue
		}

		needsUpdateCheckpoint = true

		startRPCTime := time.Now()
		// Manager.Allocate involves RPC calls to device plugin, which
		// could be heavy-weight. Therefore we want to perform this operation outside
		// mutex lock. Note if Allocate call fails, we may leave container resources
		// partially allocated for the failed container. We rely on UpdateAllocatedResources()
		// to garbage collect these resources later. Another side effect is that if
		// we have X resource A and Y resource B in total, and two containers, container1
		// and container2 both require X resource A and Y resource B. Both allocation
		// requests may fail if we serve them in mixed order.
		// TODO: may revisit this part later if we see inefficient resource allocation
		// in real use as the result of this. Should also consider to parallelize device
		// plugin Allocate grpc calls if it becomes common that a container may require
		// resources from multiple device plugins.
		m.mutex.Lock()
		eI, ok := m.endpoints[resource]
		m.mutex.Unlock()
		if !ok {
			m.mutex.Lock()
			m.allocatedResources = m.podResources.resources()
			m.mutex.Unlock()
			return fmt.Errorf("unknown Device Plugin %s", resource)
		}

		devs := allocResources.UnsortedList()
		// TODO: refactor this part of code to just append a ContainerAllocationRequest
		// in a passed in AllocateRequest pointer, and issues a single Allocate call per pod.
		klog.V(3).InfoS("Making allocation request for device plugin", "resources", devs, "resourceName", resource)
		resp, err := eI.e.allocate(devs)
		metrics.DevicePluginAllocationDuration.WithLabelValues(resource).Observe(metrics.SinceInSeconds(startRPCTime))
		if err != nil {
			// In case of allocation failure, we want to restore m.allocatedResources
			// to the actual allocated state from m.podResources.
			m.mutex.Lock()
			m.allocatedResources = m.podResources.resources()
			m.mutex.Unlock()
			return err
		}

		if len(resp.ContainerResponses) == 0 {
			return fmt.Errorf("no containers return in allocation response %v", resp)
		}

		allocResourcesWithNUMA := checkpoint.NewResourcesPerNUMA()
		// Update internal cached podResources state.
		m.mutex.Lock()
		for dev := range allocResources {
			if m.allResources[resource][dev].Topology == nil || len(m.allResources[resource][dev].Topology.Nodes) == 0 {
				allocResourcesWithNUMA[nodeWithoutTopology] = append(allocResourcesWithNUMA[nodeWithoutTopology], dev)
				continue
			}
			for idx := range m.allResources[resource][dev].Topology.Nodes {
				node := m.allResources[resource][dev].Topology.Nodes[idx]
				allocResourcesWithNUMA[node.ID] = append(allocResourcesWithNUMA[node.ID], dev)
			}
		}
		m.mutex.Unlock()
		m.podResources.insert(podUID, contName, resource, allocResourcesWithNUMA, resp.ContainerResponses[0])
	}

	if needsUpdateCheckpoint {
		return m.writeCheckpoint()
	}

	return nil
}

// checkPodActive checks if the given pod is still in activePods list
func (m *ManagerImpl) checkPodActive(pod *v1.Pod) bool {
	activePods := m.activePods()
	for _, activePod := range activePods {
		if activePod.UID == pod.UID {
			return true
		}
	}

	return false
}

// GetDeviceRunContainerOptions checks whether we have cached containerResources
// for the passed-in <pod, container> and returns its DeviceRunContainerOptions
// for the found one. An empty struct is returned in case no cached state is found.
func (m *ManagerImpl) GetDeviceRunContainerOptions(pod *v1.Pod, container *v1.Container) (*DeviceRunContainerOptions, error) {
	podUID := string(pod.UID)
	contName := container.Name
	needsReAllocate := false
	for k, v := range container.Resources.Limits {
		resource := string(k)
		if !m.isDevicePluginResource(resource) || v.Value() == 0 {
			continue
		}
		err := m.callPreStartContainerIfNeeded(podUID, contName, resource)
		if err != nil {
			return nil, err
		}

		if !m.checkPodActive(pod) {
			klog.ErrorS(nil, "pod deleted from activePods, skip to reAllocate", "podUID", podUID)
			continue
		}

		// This is a device plugin resource yet we don't have cached
		// resource state. This is likely due to a race during node
		// restart. We re-issue allocate request to cover this race.
		if m.podResources.containerResources(podUID, contName, resource) == nil {
			needsReAllocate = true
		}
	}
	if needsReAllocate {
		klog.V(2).InfoS("Needs to re-allocate device plugin resources for pod", "pod", klog.KObj(pod), "containerName", container.Name)
		if err := m.Allocate(pod, container); err != nil {
			return nil, err
		}
	}
	return m.podResources.deviceRunContainerOptions(string(pod.UID), container.Name), nil
}

// callPreStartContainerIfNeeded issues PreStartContainer grpc call for device plugin resource
// with PreStartRequired option set.
func (m *ManagerImpl) callPreStartContainerIfNeeded(podUID, contName, resource string) error {
	m.mutex.Lock()
	eI, ok := m.endpoints[resource]
	if !ok {
		m.mutex.Unlock()
		return fmt.Errorf("endpoint not found in cache for a registered resource: %s", resource)
	}

	if eI.opts == nil || !eI.opts.PreStartRequired {
		m.mutex.Unlock()
		klog.V(4).InfoS("Plugin options indicate to skip PreStartContainer for resource", "resourceName", resource)
		return nil
	}

	resources := m.podResources.containerResources(podUID, contName, resource)
	if resources == nil {
		m.mutex.Unlock()
		return fmt.Errorf("no resources found allocated in local cache for pod %s, container %s, resource %s", string(podUID), contName, resource)
	}

	m.mutex.Unlock()
	devs := resources.UnsortedList()
	klog.V(4).InfoS("Issuing a PreStartContainer call for container", "containerName", contName, "podUID", string(podUID))
	_, err := eI.e.preStartContainer(devs)
	if err != nil {
		return fmt.Errorf("device plugin PreStartContainer rpc failed with err: %v", err)
	}
	// TODO: Add metrics support for init RPC
	return nil
}

// callGetPreferredAllocationIfAvailable issues GetPreferredAllocation grpc
// call for device plugin resource with GetPreferredAllocationAvailable option set.
func (m *ManagerImpl) callGetPreferredAllocationIfAvailable(podUID, contName, resource string, available, mustInclude sets.String, size int) (sets.String, error) {
	eI, ok := m.endpoints[resource]
	if !ok {
		return nil, fmt.Errorf("endpoint not found in cache for a registered resource: %s", resource)
	}

	if eI.opts == nil || !eI.opts.GetPreferredAllocationAvailable {
		klog.V(4).InfoS("Plugin options indicate to skip GetPreferredAllocation for resource", "resourceName", resource)
		return nil, nil
	}

	m.mutex.Unlock()
	klog.V(4).InfoS("Issuing a GetPreferredAllocation call for container", "containerName", contName, "podUID", string(podUID))
	resp, err := eI.e.getPreferredAllocation(available.UnsortedList(), mustInclude.UnsortedList(), size)
	m.mutex.Lock()
	if err != nil {
		return nil, fmt.Errorf("device plugin GetPreferredAllocation rpc failed with err: %v", err)
	}
	if resp != nil && len(resp.ContainerResponses) > 0 {
		return sets.NewString(resp.ContainerResponses[0].DeviceIDs...), nil
	}
	return sets.NewString(), nil
}

// sanitizeNodeAllocatable scans through allocatedResources in the device manager
// and if necessary, updates allocatableResource in nodeInfo to at least equal to
// the allocated capacity. This allows pods that have already been scheduled on
// the node to pass GeneralPredicates admission checking even upon device plugin failure.
func (m *ManagerImpl) sanitizeNodeAllocatable(node *schedulerframework.NodeInfo) {
	var newAllocatableResource *schedulerframework.Resource
	allocatableResource := node.Allocatable
	if allocatableResource.ScalarResources == nil {
		allocatableResource.ScalarResources = make(map[v1.ResourceName]int64)
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()
	for resource, resources := range m.allocatedResources {
		needed := resources.Len()
		quant, ok := allocatableResource.ScalarResources[v1.ResourceName(resource)]
		if ok && int(quant) >= needed {
			continue
		}
		// Needs to update nodeInfo.AllocatableResource to make sure
		// NodeInfo.allocatableResource at least equal to the capacity already allocated.
		if newAllocatableResource == nil {
			newAllocatableResource = allocatableResource.Clone()
		}
		newAllocatableResource.ScalarResources[v1.ResourceName(resource)] = int64(needed)
	}
	if newAllocatableResource != nil {
		node.Allocatable = newAllocatableResource
	}
}

func (m *ManagerImpl) isDevicePluginResource(resource string) bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	_, registeredResource := m.healthyResources[resource]
	_, allocatedResource := m.allocatedResources[resource]
	// Return true if this is either an active device plugin resource or
	// a resource we have previously allocated.
	if registeredResource || allocatedResource {
		return true
	}
	return false
}

// GetAllocatableResources returns information about all the healthy resources known to the manager
func (m *ManagerImpl) GetAllocatableResources() ResourceDeviceInstances {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	resp := m.allResources.Filter(m.healthyResources)
	klog.V(4).InfoS("GetAllocatableResources", "known", len(m.allResources), "allocatable", len(resp))
	return resp
}

// GetResources returns the resources used by the specified container
func (m *ManagerImpl) GetResources(podUID, containerName string) ResourceDeviceInstances {
	return m.podResources.getContainerResources(podUID, containerName)
}

// ShouldResetExtendedResourceCapacity returns whether the extended resources should be zeroed or not,
// depending on whether the node has been recreated. Absence of the checkpoint file strongly indicates the node
// has been recreated.
func (m *ManagerImpl) ShouldResetExtendedResourceCapacity() bool {
	if utilfeature.DefaultFeatureGate.Enabled(features.DevicePlugins) {
		checkpoints, err := m.checkpointManager.ListCheckpoints()
		if err != nil {
			return false
		}
		return len(checkpoints) == 0
	}
	return false
}

func (m *ManagerImpl) setPodPendingAdmission(pod *v1.Pod) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.pendingAdmissionPod = pod
}
