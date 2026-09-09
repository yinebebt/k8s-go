IMAGE ?= ghcr.io/yinebebt/k8s-go
TAG ?= 0.2
NAMESPACE ?= k8s-go
CLUSTER ?= k8s-go
METALLB_VERSION ?= v0.15.3
METALLB_MANIFEST := https://raw.githubusercontent.com/metallb/metallb/$(METALLB_VERSION)/config/manifests/metallb-native.yaml

.PHONY: build test lint check docker-build load metallb-install deploy undeploy logs port-forward

build:
	go build -ldflags "-X main.version=$$(git rev-parse --short HEAD)" -o main .

test:
	go test ./...

lint:
	golangci-lint run -v --fix ./...

check: test lint

docker-build:
	docker build -t $(IMAGE):$(TAG) .

load: docker-build
	kind create cluster --name $(CLUSTER)
	kind load docker-image $(IMAGE):$(TAG) --name $(CLUSTER)

metallb-install:
	kubectl apply -f $(METALLB_MANIFEST)
	kubectl -n metallb-system wait --for=condition=Available deployment/controller --timeout=180s
	kubectl -n metallb-system wait --for=condition=Ready pod --all --timeout=180s

deploy: metallb-install
	kubectl apply -k k8s/
	kubectl apply -f k8s/secret.yaml
	kubectl apply -f k8s/metallb-pool.yaml
	kubectl rollout status -n $(NAMESPACE) deployment/k8s-go-deployment

undeploy:
	kubectl delete namespace $(NAMESPACE) --ignore-not-found

logs:
	kubectl logs -n $(NAMESPACE) deployment/k8s-go-deployment --all-containers=true --tail=100 -f

port-forward:
	kubectl port-forward -n $(NAMESPACE) svc/k8s-go-service 8080:80
