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

package v1alpha1

import (
        v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	intstr "k8s.io/apimachinery/pkg/util/intstr"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// NodeResourceTopology is a specification for a Foo resource
type NodeResourceTopology struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	TopologyPolicies []string `json:"topologyPolicies"`
	Zones            ZoneMap  `json:"zones"`
}

// Zone is the spec for a NodeResourceTopology resource
type Zone struct {
	Type       string           `json:"type"`
	Parent     string           `json:"parent,omitempty"`
	Costs      map[string]int   `json:"costs,omitempty"`
	Attributes map[string]int   `json:"attributes,omitempty"`
	Resources  ResourceInfoMap  `json:"resources,omitempty"`
}

type ResourceInfo struct {
	Allocatable intstr.IntOrString `json:"allocatable"`
	Capacity    intstr.IntOrString `json:"capacity"`
}

type ZoneMap map[string]Zone
type ResourceInfoMap map[v1.ResourceName]ResourceInfo

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// NodeResourceTopologyList is a list of NodeResourceTopology resources
type NodeResourceTopologyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []NodeResourceTopology `json:"items"`
}
