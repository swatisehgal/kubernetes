COMMONENVVAR=GOOS=linux GOARCH=amd64
BUILDENVVAR=CGO_ENABLED=0

RUNTIME ?= podman
REPOOWNER ?= swsehgal
IMAGENAME ?= my-scheduler
IMAGETAG ?= latest

.PHONY: gofmt
gofmt:
	@echo "Running gofmt"
	gofmt -s -w `find . -path ./vendor -prune -o -type f -name '*.go' -print`

.PHONY: govet
govet:
	@echo "Running go vet"
	go vet

.PHONY: image
image:
	@echo "building image"
	$(RUNTIME) build -f images/Dockerfile -t quay.io/$(REPOOWNER)/$(IMAGENAME):$(IMAGETAG) .

.PHONY: crd
crd:
	@echo "deploying crd"
	kubectl create -f manifests/crd-v1alpha1.yaml
	kubectl create -f manifests/worker0-crd.yaml
	kubectl create -f manifests/worker2-crd.yaml

.PHONY: push
push: image
	@echo "pushing image"
	$(RUNTIME) push quay.io/$(REPOOWNER)/$(IMAGENAME):$(IMAGETAG)

.PHONY: deploy
deploy: push
	@echo "deploying My scheduler"
	kubectl create -f manifests/my-sched.yaml

.PHONY: deploy-pod
deploy-pod:
	@echo "deploying test Pod"
	kubectl create -f manifests/test-pod.yaml

# make clean-binaries:
# 	rm -f bin/kube-scheduler

clean: clean-binaries
	kubectl delete -f manifests/my-sched.yaml
	kubectl delete -f manifests/test-pod.yaml

