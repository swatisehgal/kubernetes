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

package noderesources

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/davecgh/go-spew/spew"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/klog/v2"
	v1qos "k8s.io/kubernetes/pkg/apis/core/v1/helper/qos"
	bm "k8s.io/kubernetes/pkg/kubelet/cm/topologymanager/bitmask"
	"k8s.io/kubernetes/pkg/scheduler/apis/config"
	framework "k8s.io/kubernetes/pkg/scheduler/framework/v1alpha1"

	topologyv1alpha1 "github.com/AlexeyPerevalov/topologyapi/pkg/apis/topology/v1alpha1"
	topoclientset "github.com/AlexeyPerevalov/topologyapi/pkg/generated/clientset/versioned"
	topoinformerexternal "github.com/AlexeyPerevalov/topologyapi/pkg/generated/informers/externalversions"
	topologyinformers "github.com/AlexeyPerevalov/topologyapi/pkg/generated/informers/externalversions"
	topoinformerv1alpha1 "github.com/AlexeyPerevalov/topologyapi/pkg/generated/informers/externalversions/topology/v1alpha1"
)

const (
	// TopologyMatchName is the name of the plugin used in the plugin registry and configurations.
	TopologyMatchName = "TopologyMatch"
)

var _ framework.FilterPlugin = &TopologyMatch{}

type nodeTopologyMap map[string]topologyv1alpha1.NodeResourceTopology

// TopologyMatch plugin which run simplified version of TopologyManager's admit handler
type TopologyMatch struct {
	handle framework.FrameworkHandle

	NodeTopologyInformer    topoinformerv1alpha1.NodeResourceTopologyInformer
	TopologyInformerFactory topoinformerexternal.SharedInformerFactory
	NodeTopologies          nodeTopologyMap
	NodeTopologyGuard       sync.RWMutex
}

// Name returns name of the plugin. It is used in logs, etc.
func (tm *TopologyMatch) Name() string {
	return TopologyMatchName
}

func filter(containers []v1.Container, nodes []topologyv1alpha1.NUMANodeResource, nodeName string, qos v1.PodQOSClass) *framework.Status {
	klog.Infof("nodeName: %s Calling filter with containers %v: %v", nodeName, spew.Sdump(containers), spew.Sdump(nodes))

	if qos == v1.PodQOSBestEffort {
		return nil
	}

	zeroQuantity := resource.MustParse("0")
	for _, container := range containers {
		bitmask := bm.NewEmptyBitMask()
		bitmask.Fill()
		for resource, quantity := range container.Resources.Requests {
			klog.Infof("nodeName: %s resource %v quantity :%v", nodeName, resource, quantity)

			resourceBitmask := bm.NewEmptyBitMask()
			for _, numaNode := range nodes {
				numaQuantity, ok := numaNode.Resources[resource]
				klog.Infof("nodeName: %s Numaqty %v requested qty :%v", nodeName, numaQuantity, quantity)

				// if can't find requested resource on the node - skip (don't set it as available NUMA node)
				// if unfound resource has 0 quantity probably this numa node can be considered
				if !ok && quantity.Cmp(zeroQuantity) != 0{
					klog.Infof("nodeName: %v Going to continue: resource: %v quantity: %v", nodeName, resource, quantity)
					continue
				}
				// Check for the following:
				// 1. set numa node as possible node if resource is memory or Hugepages (until memory manager will not be merged and
				// memory will not be provided in CRD
				// 2. set numa node as possible node if resource is cpu and it's not guaranteed QoS, since cpu will flow
				// 3. set numa node as possible node if zero quantity for non existing resource was requested (TODO check topology manaager behaviour)
				// 4. otherwise check amount of resources
				if resource == v1.ResourceMemory ||
					strings.HasPrefix(string(resource), string(v1.ResourceHugePagesPrefix)) ||
					resource == v1.ResourceCPU && qos != v1.PodQOSGuaranteed ||
					quantity.Cmp(zeroQuantity) == 0 ||
					numaQuantity.Cmp(quantity) >= 0 {
						klog.Infof("nodeName: %s Adding numanode :%v resourceBitmask: %v", nodeName, numaNode.NUMAID, spew.Sdump(resourceBitmask))
						resourceBitmask.Add(numaNode.NUMAID)
						klog.Infof("nodeName: %s resourceBitmasks: %v", nodeName, spew.Sdump(bitmask))

				}
			}
			klog.Infof("nodeName: %s bitmask before AND is: %v", nodeName, bitmask)
			bitmask.And(resourceBitmask)
			klog.Infof("nodeName: %s After ANDing bitmask: %v", nodeName, spew.Sdump(bitmask))

		}
		if bitmask.IsEmpty() {
			// definitly we can't align container, so we can't align a pod
			return framework.NewStatus(framework.Unschedulable, fmt.Sprintf("Can't align container: %s", container.Name))
		}
	}
	return nil
}

// checkTopologyPolicy return true if we're working with such policy
func checkTopologyPolicy(topologyPolicy v1.TopologyManagerPolicy) bool {
	return len(topologyPolicy) > 0 && topologyPolicy == v1.SingleNUMANodeTopologyManagerPolicy
}

func getTopologyPolicy(nodeTopologies nodeTopologyMap, nodeName string) v1.TopologyManagerPolicy {
	if nodeTopology, ok := nodeTopologies[nodeName]; ok {
		return v1.TopologyManagerPolicy(nodeTopology.TopologyPolicy)
	}
	return v1.TopologyManagerPolicy("")
}

// Filter Now only single-numa-node supported
func (tm *TopologyMatch) Filter(ctx context.Context, cycleState *framework.CycleState, pod *v1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
	klog.Infof("TopologyAwareSchedulerPlugin: Filter was called for pod %s and node %+v", pod.Name, nodeInfo.Node().Name)

	if nodeInfo.Node() == nil {
		return framework.NewStatus(framework.Error, fmt.Sprintf("Node is nil %s", nodeInfo.Node().Name))
	}
	nodeName := nodeInfo.Node().Name

	// topologyPolicy := getTopologyPolicy(tm.NodeTopologies, nodeName)
	// if !checkTopologyPolicy(topologyPolicy) {
	// 	klog.V(5).Infof("Incorrect topology policy or topology policy is not specified: %s", topologyPolicy)
	// 	return nil
	// }

	containers := []v1.Container{}
	containers = append(pod.Spec.InitContainers, pod.Spec.Containers...)
	tm.NodeTopologyGuard.RLock()
	defer tm.NodeTopologyGuard.RUnlock()

	return filter(containers, tm.NodeTopologies[nodeName].Nodes, nodeName, v1qos.GetPodQOS(pod))
}

func (tm *TopologyMatch) onTopologyCRDFromDelete(obj interface{}) {
	var nodeTopology *topologyv1alpha1.NodeResourceTopology
	switch t := obj.(type) {
	case *topologyv1alpha1.NodeResourceTopology:
		nodeTopology = t
	case cache.DeletedFinalStateUnknown:
		var ok bool
		nodeTopology, ok = t.Obj.(*topologyv1alpha1.NodeResourceTopology)
		if !ok {
			klog.Errorf("cannot convert to *v1alpha1.NodeResourceTopology: %v", t.Obj)
			return
		}
	default:
		klog.Errorf("cannot convert to *v1alpha1.NodeResourceTopology: %v", t)
		return
	}

	klog.V(5).Infof("delete event for scheduled NodeResourceTopology %s/%s ",
		nodeTopology.Namespace, nodeTopology.Name)

	tm.NodeTopologyGuard.Lock()
	defer tm.NodeTopologyGuard.Unlock()
	if _, ok := tm.NodeTopologies[nodeTopology.Name]; ok {
		delete(tm.NodeTopologies, nodeTopology.Name)
	}
}

func (tm *TopologyMatch) onTopologyCRDUpdate(oldObj interface{}, newObj interface{}) {
	var nodeTopology *topologyv1alpha1.NodeResourceTopology
	switch t := newObj.(type) {
	case *topologyv1alpha1.NodeResourceTopology:
		nodeTopology = t
	case cache.DeletedFinalStateUnknown:
		var ok bool
		nodeTopology, ok = t.Obj.(*topologyv1alpha1.NodeResourceTopology)
		if !ok {
			klog.Errorf("cannot convert to *v1alpha1.NodeResourceTopology: %v", t.Obj)
			return
		}
	default:
		klog.Errorf("cannot convert to *v1alpha1.NodeResourceTopology: %v", t)
		return
	}
	klog.V(5).Infof("update event for scheduled NodeResourceTopology %s/%s ",
		nodeTopology.Namespace, nodeTopology.Name)

	tm.NodeTopologyGuard.Lock()
	defer tm.NodeTopologyGuard.Unlock()
	tm.NodeTopologies[nodeTopology.Name] = *nodeTopology
}

func (tm *TopologyMatch) onTopologyCRDAdd(obj interface{}) {
	var nodeTopology *topologyv1alpha1.NodeResourceTopology
	switch t := obj.(type) {
	case *topologyv1alpha1.NodeResourceTopology:
		nodeTopology = t
	case cache.DeletedFinalStateUnknown:
		var ok bool
		nodeTopology, ok = t.Obj.(*topologyv1alpha1.NodeResourceTopology)
		if !ok {
			klog.Errorf("cannot convert to *v1alpha1.NodeResourceTopology: %v", t.Obj)
			return
		}
	default:
		klog.Errorf("cannot convert to *v1alpha1.NodeResourceTopology: %v", t)
		return
	}
	klog.V(5).Infof("add event for scheduled NodeResourceTopology %s/%s ",
		nodeTopology.Namespace, nodeTopology.Name)

	tm.NodeTopologyGuard.Lock()
	defer tm.NodeTopologyGuard.Unlock()
	tm.NodeTopologies[nodeTopology.Name] = *nodeTopology
}

// NewTopologyMatch initializes a new plugin and returns it.
func NewTopologyMatch(args runtime.Object, handle framework.FrameworkHandle) (framework.Plugin, error) {
	klog.V(5).Infof("creating new TopologyMatch plugin")
	tcfg, ok := args.(*config.TopologyMatchArgs)
	if !ok {
		return nil, fmt.Errorf("want args to be of type TopologyMatchArgs, got %T", args)
	}

	topologyMatch := &TopologyMatch{}

	kubeConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: tcfg.KubeConfig},
		&clientcmd.ConfigOverrides{ClusterInfo: clientcmdapi.Cluster{Server: tcfg.MasterOverride}}).ClientConfig()
	if err != nil {
		klog.Errorf("Can't create kubeconfig based on: %s, %s, %v", tcfg.KubeConfig, tcfg.MasterOverride, err)
		return nil, err
	}

	topoClient, err := topoclientset.NewForConfig(kubeConfig)
	if err != nil {
		klog.Errorf("Can't create clientset for NodeTopologyResource: %s, %s", kubeConfig, err)
		return nil, err
	}

	topologyMatch.TopologyInformerFactory = topologyinformers.NewSharedInformerFactory(topoClient, 0)
	topologyMatch.NodeTopologyInformer = topologyMatch.TopologyInformerFactory.Topocontroller().V1alpha1().NodeResourceTopologies()

	topologyMatch.NodeTopologyInformer.Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    topologyMatch.onTopologyCRDAdd,
			UpdateFunc: topologyMatch.onTopologyCRDUpdate,
			DeleteFunc: topologyMatch.onTopologyCRDFromDelete,
		},
	)

	go topologyMatch.NodeTopologyInformer.Informer().Run(context.Background().Done())
	topologyMatch.TopologyInformerFactory.Start(context.Background().Done())

	klog.V(5).Infof("start NodeTopologyInformer")

	topologyMatch.handle = handle
	topologyMatch.NodeTopologies = nodeTopologyMap{}

	return topologyMatch, nil
}
