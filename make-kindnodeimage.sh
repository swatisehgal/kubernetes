#!/bin/bash
kind delete cluster
# docker rmi -f quay.io/swsehgal/smtaware-kind-node:latest
#  docker images -a |  grep "node" | awk '{print $3}' | xargs docker rmi -f
# ssh root@192.168.1.69 'docker rmi quay.io/swsehgal/smtaware-kind-node:latest;docker rmi kindest/node:latest'
# ssh root@192.168.1.69 'kind build node-image --kube-root=/root/go/src/k8s.io/kubernetes'
# ssh root@192.168.1.69 'docker tag kindest/node:latest  quay.io/swsehgal/smtaware-kind-node'
# ssh root@192.168.1.69 'docker push quay.io/swsehgal/smtaware-kind-node'
docker rmi -f quay.io/swsehgal/smtaware-node:latest;docker rmi -f kindest/node:latest
kind build node-image --kube-root=/root/go/src/k8s.io/kubernetes
docker tag kindest/node:latest  quay.io/swsehgal/smtaware-kind-node
docker push quay.io/swsehgal/smtaware-kind-node
# docker rmi quay.io/swsehgal/smtaware-kind-node:latest
kind create cluster --config=kind-2-smtaware.yaml
kubectl taint nodes kind-control-plane node-role.kubernetes.io/master-
# kubectl create -f test-deployment.yaml
# kind export logs
