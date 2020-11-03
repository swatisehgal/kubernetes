module k8s.io/noderesourcetopology-api

go 1.15

require (
	k8s.io/api v0.19.3
	k8s.io/apimachinery v0.19.3
	k8s.io/code-generator v0.18.6
	sigs.k8s.io/structured-merge-diff/v3 v3.0.0 // indirect
)

replace k8s.io/noderesourcetopology-api => ../noderesourcetopology-api
