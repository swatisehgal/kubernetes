
kubectl edit clusterrole system:kube-scheduler

resourceNames:
- kube-scheduler
- my-scheduler
resources:
- leases
verbs:
- get
- update
- apiGroups:
- ""
resourceNames:
- kube-scheduler
- my-scheduler




./build.sh to compile kubernetes and copy binary to this repo
make push
make deploy



/usr/local/bin/kube-scheduler --address=0.0.0.0 --leader-elect=true --lock-object-namespace=kube-system --lock-object-name=my-scheduler --scheduler-name=my-scheduler
