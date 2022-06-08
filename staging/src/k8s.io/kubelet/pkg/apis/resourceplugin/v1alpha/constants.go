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

package resourceplugin

const (
	// Healthy means that the resource is healthy
	Healthy = "Healthy"
	// Unhealthy means that the resource is unhealthy
	Unhealthy = "Unhealthy"

	// Version means current version of the API supported by kubelet
	Version = "v1beta1"
	// ResourcePluginPath is the folder the Resource Plugin is expecting sockets to be on
	// Only privileged pods have access to this path
	// Note: Placeholder until we find a "standard path"
	ResourcePluginPath = "/var/lib/kubelet/resource-plugins/"
	// KubeletSocket is the path of the Kubelet registry socket
	KubeletSocket = ResourcePluginPath + "kubelet.sock"

	// ResourcePluginPathWindows Avoid failed to run Kubelet: bad socketPath,
	// must be an absolute path: /var/lib/kubelet/resource-plugins/kubelet.sock
	// https://github.com/kubernetes/kubernetes/issues/93262
	// https://github.com/kubernetes/kubernetes/pull/93285#discussion_r458140701
	ResourcePluginPathWindows = "\\var\\lib\\kubelet\\resource-plugins\\"
	// KubeletSocketWindows is the path of the Kubelet registry socket on windows
	KubeletSocketWindows = ResourcePluginPathWindows + "kubelet.sock"

	// KubeletPreStartContainerRPCTimeoutInSecs is the timeout duration in secs for PreStartContainer RPC
	// Timeout duration in secs for PreStartContainer RPC
	KubeletPreStartContainerRPCTimeoutInSecs = 30
)

// SupportedVersions provides a list of supported version
var SupportedVersions = [...]string{"v1alpha"}
