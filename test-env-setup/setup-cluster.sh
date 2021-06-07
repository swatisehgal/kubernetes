#!/bin/bash
kind delete cluster
docker rmi -f quay.io/swsehgal/kind-podresource-node:latest;docker rmi -f kindest/node:latest
kind build node-image --kube-root=/root/go/src/k8s.io/kubernetes
docker tag kindest/node:latest  quay.io/swsehgal/kind-podresource-node
docker push quay.io/swsehgal/kind-podresource-node:latest
kind create cluster --config=kind-podresource.yaml
kubectl taint nodes kind-control-plane node-role.kubernetes.io/master-