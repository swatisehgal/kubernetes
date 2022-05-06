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

package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"k8s.io/klog/v2"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
	plugin "k8s.io/kubernetes/pkg/kubelet/cm/devicemanager/plugin/v1beta1"
)

const (
	resourceName = "example.com/resource"
)

// stubAllocFunc creates and returns allocation response for the input allocate request
func stubAllocFunc(r *pluginapi.AllocateRequest, devs map[string]pluginapi.Device) (*pluginapi.AllocateResponse, error) {
	var responses pluginapi.AllocateResponse
	for _, req := range r.ContainerRequests {
		var deviceIDs []string
		response := &pluginapi.ContainerAllocateResponse{}
		for _, requestID := range req.DevicesIDs {
			dev, ok := devs[requestID]
			if !ok {
				return nil, fmt.Errorf("invalid allocation request with non-existing device %s", requestID)
			}

			if dev.Health != pluginapi.Healthy {
				return nil, fmt.Errorf("invalid allocation request with unhealthy device: %s", requestID)
			}

			// create fake device file
			fpath := filepath.Join("/tmp", dev.ID)

			// clean first
			if err := os.RemoveAll(fpath); err != nil {
				return nil, fmt.Errorf("failed to clean fake device file from previous run: %s", err)
			}

			f, err := os.Create(fpath)
			if err != nil && !os.IsExist(err) {
				return nil, fmt.Errorf("failed to create fake device file: %s", err)
			}

			f.Close()

			deviceIDs = append(deviceIDs, dev.ID)

			response.Mounts = append(response.Mounts, &pluginapi.Mount{
				ContainerPath: fpath,
				HostPath:      fpath,
			})
		}
		response.Envs = map[string]string{
			"SAMPLE_DEVICES": strings.Join(deviceIDs, ","),
		}
		responses.ContainerResponses = append(responses.ContainerResponses, response)
	}

	return &responses, nil
}

func main() {
	var autoRegister bool
	var err error

	devs := []*pluginapi.Device{
		{ID: "Dev-1", Health: pluginapi.Healthy},
		{ID: "Dev-2", Health: pluginapi.Healthy},
	}

	pluginSocksDir := os.Getenv("PLUGIN_SOCK_DIR")
	klog.Infof("pluginSocksDir: %s", pluginSocksDir)
	if pluginSocksDir == "" {
		klog.Errorf("Empty pluginSocksDir")
		return
	}

	autoRegisterString := os.Getenv("AUTO_REGISTER")
	if autoRegisterString == "" {
		autoRegister = true
	} else {

		autoRegister, err = strconv.ParseBool(autoRegisterString)
		if err != nil {
			klog.Errorf("invalid boolean value specified")
			panic(err)
		}

	}

	socketPath := pluginSocksDir + "/dp." + fmt.Sprintf("%d", time.Now().Unix())
	triggerPath := pluginSocksDir + "/registered"

	dp1 := plugin.NewDevicePluginStub(devs, socketPath, resourceName, false, false)
	if err := dp1.Start(); err != nil {
		panic(err)

	}
	dp1.SetAllocFunc(stubAllocFunc)

	if !autoRegister {
		l, err := net.Listen("unix", triggerPath)
		klog.Infof("Started Listening at %s", triggerPath)
		if err != nil {
			panic(err)
		}
		defer l.Close()

		sigc := make(chan os.Signal, 1)
		signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
		go func(ln net.Listener, c chan os.Signal) {
			sig := <-c
			klog.Infof("Received termination signal %s: shutting down.", sig)
			ln.Close()
			os.Exit(0)
		}(l, sigc)

		for {
			klog.InfoS("Starting to accept connections")
			// Accept new connections
			conn, err := l.Accept()
			if err != nil {
				panic(err)
			}
			defer conn.Close()
			io.Copy(conn, conn)
			klog.InfoS("Client connected!")
			break
		}
	}
	if err := dp1.Register(pluginapi.KubeletSocket, resourceName, pluginapi.DevicePluginPath); err != nil {
		panic(err)
	}
	select {}
}
