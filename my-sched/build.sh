#!/bin/bash
cd /root/go/src/k8s.io/kubernetes-topo-aware-scheduler
make
cp ./_output/local/bin/linux/amd64/kube-scheduler my-sched/bin/
cd my-sched
# https://kubernetes.io/docs/tasks/extend-kubernetes/configure-multiple-schedulers/
# /usr/local/bin/kube-scheduler --address=0.0.0.0 --leader-elect=false --scheduler-name=my-scheduler
# /usr/local/bin/kube-scheduler --address=0.0.0.0 --leader-elect=true --lock-object-namespace=kube-system --lock-object-name=my-scheduler --scheduler-name=my-scheduler
