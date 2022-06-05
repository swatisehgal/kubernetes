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

package checkpoint

import (
	"encoding/json"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/kubernetes/pkg/kubelet/checkpointmanager"
	"k8s.io/kubernetes/pkg/kubelet/checkpointmanager/checksum"
)

// ResourceManagerCheckpoint defines the operations to retrieve pod resources
type ResourceManagerCheckpoint interface {
	checkpointmanager.Checkpoint
	GetData() ([]PodResourcesEntry, map[string]map[string][]string)
}

// ResourcesPerNUMA represents resource names obtained from resource plugin per NUMA node id
// mapping NUMA id -> ResourceProviders (CPU/Memory/Device/External) -> resources
type ResourcesPerNUMA map[int64]map[string][]string

// PodResourcesEntry connects pod information to resources
type PodResourcesEntry struct {
	PodUID        string
	ContainerName string
	ResourceName  string
	ResourcesIDs  ResourcesPerNUMA
	AllocResp     []byte
}

// checkpointData struct is used to store pod to resource allocation information
// in a checkpoint file.
// TODO: add version control when we need to change checkpoint format.
type checkpointData struct {
	PodResourcesEntries []PodResourcesEntry
	RegisteredResources map[string]map[string][]string
}

// Data holds checkpoint data and its checksum
type Data struct {
	Data     checkpointData
	Checksum checksum.Checksum
}

// NewResourcesPerNUMA is a function that creates ResourcesPerNUMA map
func NewResourcesPerNUMA() ResourcesPerNUMA {
	return make(ResourcesPerNUMA)
}

// Resources is a function that returns all resource ids for all NUMA nodes
// and represent it as sets.String
func (res ResourcesPerNUMA) Resources() sets.String {
	result := sets.NewString()

	for _, resources := range res {
		for _, res := range resources {
			result.Insert(res...)
		}
	}
	return result
}

// New returns an instance of Checkpoint - must be an alias for the most recent version
func New(resEntries []PodResourcesEntry, resources map[string]map[string][]string) ResourceManagerCheckpoint {
	return &Data{
		Data: checkpointData{
			PodResourcesEntries: resEntries,
			RegisteredResources: resources,
		},
	}
}

// MarshalCheckpoint returns marshalled data
func (cp *Data) MarshalCheckpoint() ([]byte, error) {
	cp.Checksum = checksum.New(cp.Data)
	return json.Marshal(*cp)
}

// UnmarshalCheckpoint returns unmarshalled data
func (cp *Data) UnmarshalCheckpoint(blob []byte) error {
	return json.Unmarshal(blob, cp)
}

// VerifyChecksum verifies that passed checksum is same as calculated checksum
func (cp *Data) VerifyChecksum() error {
	return cp.Checksum.Verify(cp.Data)
}

// GetData returns resource entries and registered resources.
func (cp *Data) GetData() ([]PodResourcesEntry, map[string]map[string][]string) {
	return cp.Data.PodResourcesEntries, cp.Data.RegisteredResources
}
