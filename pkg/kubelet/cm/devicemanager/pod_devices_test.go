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

package devicemanager

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetContainerDevices(t *testing.T) {
	podDevices := make(podDevices)
	resourceName1 := "domain1.com/resource1"
	podId := "pod1"
	contId := "con1"
	devices := devicePerNUMA{0: []string{"dev1"}, 1: []string{"dev1"}}

	podDevices.insert(podId, contId, resourceName1,
		devices,
		constructAllocResp(map[string]string{"/dev/r1dev1": "/dev/r1dev1", "/dev/r1dev2": "/dev/r1dev2"}, map[string]string{"/home/r1lib1": "/usr/r1lib1"}, map[string]string{}))

	contDevices := podDevices.getContainerDevices(podId, contId)
	require.Equal(t, len(devices), len(contDevices), "Incorrect container devices")
	for _, contDev := range contDevices {
		for _,node := range contDev.Topology.Nodes {
			dev, ok := devices[int(node.ID)]
			require.True(t, ok, "NUMA id %d doesn't exist in result", int(node.ID))
			require.Equal(t, contDev.DeviceIds[0], dev[0], "Can't find device %s in result", dev[0])
		}
	}
}
